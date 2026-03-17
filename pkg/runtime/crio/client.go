package crio

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
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
	if r.conn != nil {
		return nil
	}
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
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// ContainerRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListContainers(ctx context.Context) ([]runtime.Container, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}
	resp, err := r.runtimeClient.ListContainers(ctx, &runtimeapi.ListContainersRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	result := make([]runtime.Container, 0, len(resp.GetContainers()))
	for _, c := range resp.GetContainers() {
		result = append(result, r.newContainerHandle(c))
	}
	return result, nil
}

func (r *Runtime) GetContainer(ctx context.Context, id string) (runtime.Container, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}
	// CRI does not have a "get single container" — list with filter.
	resp, err := r.runtimeClient.ListContainers(ctx, &runtimeapi.ListContainersRequest{
		Filter: &runtimeapi.ContainerFilter{Id: id},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get container %s: %w", id, err)
	}
	if len(resp.GetContainers()) == 0 {
		return nil, fmt.Errorf("container %s not found", id)
	}
	return r.newContainerHandle(resp.GetContainers()[0]), nil
}

// ---------------------------------------------------------------------------
// ImageRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListImages(ctx context.Context) ([]runtime.Image, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}
	resp, err := r.imageClient.ListImages(ctx, &runtimeapi.ListImagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	result := make([]runtime.Image, 0, len(resp.GetImages()))
	for _, img := range resp.GetImages() {
		result = append(result, r.newImageHandle(img))
	}
	return result, nil
}

func (r *Runtime) GetImage(ctx context.Context, ref string) (runtime.Image, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}
	resp, err := r.imageClient.ImageStatus(ctx, &runtimeapi.ImageStatusRequest{
		Image:   &runtimeapi.ImageSpec{Image: ref},
		Verbose: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", ref, err)
	}
	if resp.GetImage() == nil {
		return nil, fmt.Errorf("image %s not found", ref)
	}
	return r.newImageHandle(resp.GetImage()), nil
}

// ---------------------------------------------------------------------------
// PodRuntime
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

// Compile-time interface check.
var _ runtime.Runtime = (*Runtime)(nil)
