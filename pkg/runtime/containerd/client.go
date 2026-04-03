package containerd

import (
	"context"
	"fmt"
	"sort"

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

// imageServiceAPI is the subset of *client.Client methods used by image resolution.
// Defined as an interface so that GetImage alias lookup can be unit-tested
// without a live containerd socket.
type imageServiceAPI interface {
	GetImage(ctx context.Context, ref string) (client.Image, error)
	ListImages(ctx context.Context, filters ...string) ([]client.Image, error)
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
	paths            containerdPaths
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
	r.paths = resolveContainerdPaths(ctx, c, r.procReader)
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
	for _, group := range groupImagesByDigest(imgs) {
		result = append(result, newGroupedImageHandle(r, group.raw, group.names))
	}
	return result, nil
}

func (r *Runtime) GetImage(ctx context.Context, ref string) (runtime.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}
	return resolveImageWithAliases(ctx, r.client, r, ref)
}

// resolveImageWithAliases fetches the image identified by ref and enriches its
// handle with all other names that share the same content digest. Separating
// this from GetImage keeps the alias-lookup logic independently testable via
// imageServiceAPI without requiring a live containerd socket.
func resolveImageWithAliases(ctx context.Context, svc imageServiceAPI, rt *Runtime, ref string) (runtime.Image, error) {
	img, err := svc.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", ref, err)
	}
	if img.Target().Digest == "" {
		return newImageHandle(rt, img), nil
	}

	matched, err := svc.ListImages(ctx, "target.digest=="+img.Target().Digest.String())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image aliases for %s: %w", ref, err)
	}
	for _, group := range groupImagesByDigest(matched) {
		if group.digest == img.Target().Digest.String() {
			return newGroupedImageHandle(rt, img, group.names), nil
		}
	}
	return newImageHandle(rt, img), nil
}

type imageGroup struct {
	digest string
	raw    client.Image
	names  []string
}

func groupImagesByDigest(imgs []client.Image) []imageGroup {
	groups := make(map[string]*imageGroup, len(imgs))
	order := make([]string, 0, len(imgs))

	for _, img := range imgs {
		key := img.Target().Digest.String()
		if key == "" {
			key = "name:" + img.Name()
		}
		group := groups[key]
		if group == nil {
			group = &imageGroup{digest: img.Target().Digest.String(), raw: img}
			groups[key] = group
			order = append(order, key)
		}
		group.names = append(group.names, img.Name())
	}

	result := make([]imageGroup, 0, len(order))
	for _, key := range order {
		group := groups[key]
		sort.Strings(group.names)
		result = append(result, *group)
	}
	return result
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
