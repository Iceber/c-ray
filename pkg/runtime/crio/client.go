package crio

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	cstorage "go.podman.io/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	defaultStorageRoot    = "/var/lib/containers/storage"
	defaultStorageRunRoot = "/run/containers/storage"
	defaultGraphDriver    = "overlay"
)

// Runtime implements runtime.Runtime backed by CRI-O.
//
// containers/storage is the primary data source for containers, images, and
// layers. CRI supplements runtime state such as PID, lifecycle status, labels,
// pod association, and network metadata.
type Runtime struct {
	config *runtime.Config

	conn          *grpc.ClientConn
	runtimeClient runtimeapi.RuntimeServiceClient
	imageClient   runtimeapi.ImageServiceClient

	// Shared CRI metadata client for mount, status, and network operations.
	criClient *cri.MetadataClient

	processCollector *sysinfo.ProcessCollector
	procReader       *sysinfo.ProcReader
	cgroupReader     *sysinfo.CGroupReader
	mountReader      *sysinfo.MountReader

	storageRoot    string
	storageRunRoot string

	storeOnce sync.Once
	store     cstorage.Store
	storeErr  error
}

// New creates a new CRI-O backed runtime.
func New(config *runtime.Config) *Runtime {
	processCollector, _ := sysinfo.NewProcessCollector()
	cgroupReader, _ := sysinfo.NewCGroupReader()

	storageRoot := config.StorageRoot
	if storageRoot == "" {
		storageRoot = defaultStorageRoot
	}
	storageRunRoot := config.StorageRunRoot
	if storageRunRoot == "" {
		storageRunRoot = defaultStorageRunRoot
	}

	return &Runtime{
		config:           config,
		criClient:        cri.NewMetadataClient(config.SocketPath),
		processCollector: processCollector,
		procReader:       sysinfo.NewProcReader(),
		cgroupReader:     cgroupReader,
		mountReader:      sysinfo.NewMountReader(),
		storageRoot:      storageRoot,
		storageRunRoot:   storageRunRoot,
	}
}

func (r *Runtime) Connect(ctx context.Context) error {
	// Initialize containers/storage (primary data source).
	if _, err := r.getStore(); err != nil {
		return fmt.Errorf("open containers/storage: %w", err)
	}

	if r.conn != nil {
		return nil
	}

	// Establish CRI connection for supplementary runtime data.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, r.config.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", r.config.SocketPath)
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to CRI-O at %s: %w", r.config.SocketPath, err)
	}
	r.conn = conn
	r.runtimeClient = runtimeapi.NewRuntimeServiceClient(conn)
	r.imageClient = runtimeapi.NewImageServiceClient(conn)
	return nil
}

func (r *Runtime) Close() error {
	if r.store != nil {
		r.store.Free()
		r.store = nil
	}
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// ContainerRuntime — backed by containers/storage + CRI supplement
// ---------------------------------------------------------------------------

func (r *Runtime) ListContainers(ctx context.Context) ([]runtime.Container, error) {
	store, err := r.getStore()
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	containers, err := store.Containers()
	if err != nil {
		return nil, fmt.Errorf("list storage containers: %w", err)
	}

	// Build CRI supplement index.
	criIndex := r.buildCRIContainerIndex(ctx)

	result := make([]runtime.Container, 0, len(containers))
	for i := range containers {
		result = append(result, r.newContainerHandle(&containers[i], criIndex[containers[i].ID]))
	}
	return result, nil
}

func (r *Runtime) GetContainer(ctx context.Context, id string) (runtime.Container, error) {
	store, err := r.getStore()
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	ctr, err := store.Container(id)
	if err != nil {
		return nil, fmt.Errorf("lookup container %s: %w", id, err)
	}

	// Try CRI supplement for this specific container.
	var supplement *criContainerSupplement
	if r.conn != nil {
		resp, cErr := r.runtimeClient.ListContainers(ctx, &runtimeapi.ListContainersRequest{
			Filter: &runtimeapi.ContainerFilter{Id: id},
		})
		if cErr == nil && len(resp.GetContainers()) > 0 {
			supplement = extractCRISupplement(resp.GetContainers()[0])
		}
	}

	return r.newContainerHandle(ctr, supplement), nil
}

// ---------------------------------------------------------------------------
// ImageRuntime — backed by containers/storage
// ---------------------------------------------------------------------------

func (r *Runtime) ListImages(_ context.Context) ([]runtime.Image, error) {
	store, err := r.getStore()
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	images, err := store.Images()
	if err != nil {
		return nil, fmt.Errorf("list storage images: %w", err)
	}

	result := make([]runtime.Image, 0, len(images))
	for i := range images {
		result = append(result, r.newImageHandle(&images[i]))
	}
	return result, nil
}

func (r *Runtime) GetImage(_ context.Context, ref string) (runtime.Image, error) {
	store, err := r.getStore()
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	img, err := store.Image(ref)
	if err != nil {
		return nil, fmt.Errorf("lookup image %s: %w", ref, err)
	}
	return r.newImageHandle(img), nil
}

// ---------------------------------------------------------------------------
// PodRuntime — CRI-based (no pod concept in containers/storage)
// ---------------------------------------------------------------------------

func (r *Runtime) ListPods(ctx context.Context) ([]runtime.Pod, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// List sandboxes directly from CRI.
	sandboxResp, err := r.runtimeClient.ListPodSandbox(ctx, &runtimeapi.ListPodSandboxRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pod sandboxes: %w", err)
	}

	// List all containers to associate with pods.
	containers, err := r.ListContainers(ctx)
	if err != nil {
		return nil, err
	}

	// Build sandbox → containers map.
	containersByPod := make(map[string][]runtime.Container)
	for _, c := range containers {
		info, err := c.Info(ctx)
		if err != nil {
			continue
		}
		if info.PodUID != "" {
			containersByPod[info.PodUID] = append(containersByPod[info.PodUID], c)
		}
	}

	result := make([]runtime.Pod, 0, len(sandboxResp.GetItems()))
	for _, sb := range sandboxResp.GetItems() {
		meta := sb.GetMetadata()
		if meta == nil {
			continue
		}
		uid := meta.GetUid()
		ph := &podHandle{
			uid: uid,
			info: &runtime.PodInfo{
				Name:      meta.GetName(),
				Namespace: meta.GetNamespace(),
				UID:       uid,
			},
			containers: containersByPod[uid],
		}
		result = append(result, ph)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// CRI supplement helpers
// ---------------------------------------------------------------------------

// buildCRIContainerIndex fetches all CRI containers and indexes them by ID
// for efficient supplementation of storage containers.
func (r *Runtime) buildCRIContainerIndex(ctx context.Context) map[string]*criContainerSupplement {
	if r.conn == nil {
		return nil
	}
	resp, err := r.runtimeClient.ListContainers(ctx, &runtimeapi.ListContainersRequest{})
	if err != nil {
		return nil
	}
	index := make(map[string]*criContainerSupplement, len(resp.GetContainers()))
	for _, c := range resp.GetContainers() {
		index[c.GetId()] = extractCRISupplement(c)
	}
	return index
}

// extractCRISupplement extracts supplementary data from a CRI container.
func extractCRISupplement(c *runtimeapi.Container) *criContainerSupplement {
	s := &criContainerSupplement{
		podSandboxID: c.GetPodSandboxId(),
		image:        c.GetImage().GetImage(),
		imageRef:     c.GetImageRef(),
		labels:       c.GetLabels(),
		annotations:  c.GetAnnotations(),
	}
	if meta := c.GetMetadata(); meta != nil {
		s.name = meta.GetName()
	}
	return s
}

// Compile-time interface check.
var _ runtime.Runtime = (*Runtime)(nil)
