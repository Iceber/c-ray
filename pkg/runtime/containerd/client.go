package containerd

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/client"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

type criMetadataClient interface {
	InspectContainerMounts(ctx context.Context, containerID string) (*cri.ContainerMountSet, error)
	ApplyPodSandboxNetwork(ctx context.Context, sandboxID string, dst *runtime.PodNetworkInfo) error
	InspectContainerStatus(ctx context.Context, containerID string) (*cri.ContainerStatus, error)
}

// Runtime implements runtime.Runtime backed by containerd.
type Runtime struct {
	config           *runtime.Config
	client           *client.Client
	processCollector *sysinfo.ProcessCollector
	procReader       *sysinfo.ProcReader
	cgroupReader     *sysinfo.CGroupReader
	mountReader      *sysinfo.MountReader
	criClient        criMetadataClient
}

// New creates a new containerd-backed runtime.
func New(config *runtime.Config) *Runtime {
	processCollector, _ := sysinfo.NewProcessCollector()
	cgroupReader, _ := sysinfo.NewCGroupReader()

	return &Runtime{
		config:           config,
		processCollector: processCollector,
		procReader:       sysinfo.NewProcReader(),
		cgroupReader:     cgroupReader,
		mountReader:      sysinfo.NewMountReader(),
		criClient:        cri.NewMetadataClient(config.SocketPath),
	}
}

func (r *Runtime) Connect(ctx context.Context) error {
	if r.client != nil {
		return nil
	}
	c, err := client.New(
		r.config.SocketPath,
		client.WithDefaultNamespace(r.config.Namespace),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to containerd at %s: %w", r.config.SocketPath, err)
	}
	r.client = c
	return nil
}

func (r *Runtime) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// ContainerRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListContainers(ctx context.Context) ([]runtime.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	result := make([]runtime.Container, 0, len(containers))
	for _, c := range containers {
		h, err := r.newContainerHandle(ctx, c)
		if err != nil {
			continue
		}
		result = append(result, h)
	}
	return result, nil
}

func (r *Runtime) GetContainer(ctx context.Context, id string) (runtime.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}
	c, err := r.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}
	return r.newContainerHandle(ctx, c)
}

// ---------------------------------------------------------------------------
// ImageRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListImages(ctx context.Context) ([]runtime.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}
	imgs, err := r.client.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	result := make([]runtime.Image, 0, len(imgs))
	for _, img := range imgs {
		result = append(result, newImageHandle(r, img))
	}
	return result, nil
}

func (r *Runtime) GetImage(ctx context.Context, ref string) (runtime.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}
	img, err := r.client.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", ref, err)
	}
	return newImageHandle(r, img), nil
}

// ---------------------------------------------------------------------------
// PodRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListPods(ctx context.Context) ([]runtime.Pod, error) {
	containers, err := r.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	podMap := make(map[string]*podHandle)
	for _, c := range containers {
		info, err := c.Info(ctx)
		if err != nil || info.PodUID == "" {
			continue
		}
		ph, exists := podMap[info.PodUID]
		if !exists {
			ph = &podHandle{
				uid: info.PodUID,
				info: &runtime.PodInfo{
					Name:      info.PodName,
					Namespace: info.PodNamespace,
					UID:       info.PodUID,
				},
			}
			podMap[info.PodUID] = ph
		}
		ph.containers = append(ph.containers, c)
	}
	result := make([]runtime.Pod, 0, len(podMap))
	for _, ph := range podMap {
		result = append(result, ph)
	}
	return result, nil
}

// Compile-time interface check.
var _ runtime.Runtime = (*Runtime)(nil)
