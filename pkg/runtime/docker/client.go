package docker

import (
	"context"
	"fmt"
	"os"
	"strings"

	refdocker "github.com/distribution/reference"
	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	"github.com/icebergu/c-ray/pkg/runtime"
	containerdrt "github.com/icebergu/c-ray/pkg/runtime/containerd"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

// Runtime implements runtime.Runtime backed by the Docker Engine API.
//
// Container and pod operations always go through the Docker daemon.
// Image operations are delegated based on the detected image store mode:
//   - Classic: images are managed via Docker's traditional graphdriver model.
//   - Containerd: images are managed by containerd; we delegate to the
//     existing containerd runtime for image-related methods.
type Runtime struct {
	config       *runtime.Config
	dockerClient *dockerclient.Client

	processCollector *sysinfo.ProcessCollector
	procReader       *sysinfo.ProcReader
	cgroupReader     *sysinfo.CGroupReader
	mountReader      *sysinfo.MountReader

	// Cached daemon info, populated at Connect time.
	daemonInfo     *daemonInfo
	imageStoreMode ImageStoreMode

	// containerdRT is used to delegate image operations when the Docker daemon
	// uses the containerd image store.
	//
	// TODO: Extract a reusable image-only module from pkg/runtime/containerd
	// instead of composing the full containerd.Runtime. This avoids unnecessary
	// container/pod capability initialization and makes the dependency more
	// explicit. For now, we create a full containerd.Runtime but only call its
	// ImageRuntime methods.
	containerdRT *containerdrt.Runtime
}

// New creates a new Docker-backed runtime.
func New(config *runtime.Config) *Runtime {
	processCollector, _ := sysinfo.NewProcessCollector()
	cgroupReader, _ := sysinfo.NewCGroupReader()

	return &Runtime{
		config:           config,
		processCollector: processCollector,
		procReader:       sysinfo.NewProcReader(),
		cgroupReader:     cgroupReader,
		mountReader:      sysinfo.NewMountReader(),
	}
}

func (r *Runtime) Connect(ctx context.Context) error {
	if r.dockerClient != nil {
		return nil
	}

	// Create Docker client using the configured socket path.
	opts := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}
	if r.config.SocketPath != "" {
		opts = append(opts, dockerclient.WithHost("unix://"+r.config.SocketPath))
	} else {
		opts = append(opts, dockerclient.FromEnv)
	}

	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	r.dockerClient = cli

	// Probe daemon info and determine image store mode.
	di, mode, err := r.probeDaemon(ctx)
	if err != nil {
		r.dockerClient.Close()
		r.dockerClient = nil
		return fmt.Errorf("failed to probe docker daemon: %w", err)
	}
	r.daemonInfo = di
	r.imageStoreMode = mode

	fmt.Fprintf(os.Stderr, "[docker] connected (version: %s, driver: %s, image-store: %s)\n",
		di.ServerVersion, di.Driver, mode)

	// If containerd image store is detected, create the containerd delegate.
	if mode == ImageStoreModeContainerd {
		if err := r.initContainerdDelegate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[docker] warning: containerd delegate init failed, falling back to summary-only images: %v\n", err)
			// Non-fatal: container operations still work; image depth is reduced.
			r.imageStoreMode = ImageStoreModeClassic
		}
	}

	return nil
}

// initContainerdDelegate creates and connects a containerd runtime for image delegation.
func (r *Runtime) initContainerdDelegate(ctx context.Context) error {
	addr := r.daemonInfo.ContainerdAddr
	if addr == "" {
		// Try default containerd socket used by Docker.
		addr = "/run/containerd/containerd.sock"
	}

	namespace := "moby"
	if ns, ok := r.daemonInfo.ContainerdNS["containers"]; ok && ns != "" {
		namespace = ns
	}

	ctdConfig := &runtime.Config{
		SocketPath: addr,
		Namespace:  namespace,
		Timeout:    r.config.Timeout,
	}

	rt := containerdrt.New(ctdConfig)
	if err := rt.Connect(ctx); err != nil {
		return fmt.Errorf("containerd delegate connect (socket=%s ns=%s): %w", addr, namespace, err)
	}

	r.containerdRT = rt
	fmt.Fprintf(os.Stderr, "[docker] containerd image delegate connected (socket: %s, namespace: %s)\n", addr, namespace)
	return nil
}

func (r *Runtime) Close() error {
	if r.containerdRT != nil {
		r.containerdRT.Close()
		r.containerdRT = nil
	}
	if r.dockerClient != nil {
		return r.dockerClient.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// ContainerRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListContainers(ctx context.Context) ([]runtime.Container, error) {
	if r.dockerClient == nil {
		return nil, fmt.Errorf("docker client not connected")
	}

	containers, err := r.listDockerContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list docker containers: %w", err)
	}

	result := make([]runtime.Container, 0, len(containers))
	for i := range containers {
		h := r.newContainerHandle(&containers[i])
		result = append(result, h)
	}
	return result, nil
}

func (r *Runtime) GetContainer(ctx context.Context, id string) (runtime.Container, error) {
	if r.dockerClient == nil {
		return nil, fmt.Errorf("docker client not connected")
	}

	inspection, err := r.dockerClient.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", id, err)
	}
	return r.newContainerHandleFromInspect(&inspection), nil
}

// ---------------------------------------------------------------------------
// ImageRuntime
// ---------------------------------------------------------------------------

func (r *Runtime) ListImages(ctx context.Context) ([]runtime.Image, error) {
	if r.dockerClient == nil {
		return nil, fmt.Errorf("docker client not connected")
	}

	// Delegate to containerd when using containerd image store.
	if r.imageStoreMode == ImageStoreModeContainerd && r.containerdRT != nil {
		return r.containerdRT.ListImages(ctx)
	}

	return r.listClassicImages(ctx)
}

func (r *Runtime) GetImage(ctx context.Context, ref string) (runtime.Image, error) {
	if r.dockerClient == nil {
		return nil, fmt.Errorf("docker client not connected")
	}

	if r.imageStoreMode == ImageStoreModeContainerd && r.containerdRT != nil {
		return r.getContainerdBackedImage(ctx, ref)
	}

	return r.getClassicImage(ctx, ref)
}

func (r *Runtime) getContainerdBackedImage(ctx context.Context, ref string) (runtime.Image, error) {
	candidates := r.containerdImageCandidates(ctx, ref)
	var lastErr error

	for _, candidate := range candidates {
		img, err := r.containerdRT.GetImage(ctx, candidate)
		if err == nil {
			return img, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("resolve docker image %q via containerd delegate: %w", ref, lastErr)
	}
	return nil, fmt.Errorf("resolve docker image %q via containerd delegate: no candidates", ref)
}

func (r *Runtime) containerdImageCandidates(ctx context.Context, ref string) []string {
	seen := make(map[string]struct{})
	var candidates []string
	appendCandidate := func(value string) {
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	appendNormalized := func(value string) {
		appendCandidate(value)
		if normalized := normalizeDockerImageRef(value); normalized != "" {
			appendCandidate(normalized)
		}
	}

	appendNormalized(ref)

	inspect, err := r.inspectDockerImage(ctx, ref)
	if err != nil {
		return candidates
	}

	appendCandidate(inspect.ID)
	for _, tag := range inspect.RepoTags {
		appendNormalized(tag)
	}
	for _, digest := range inspect.RepoDigests {
		appendNormalized(digest)
	}

	return candidates
}

func (r *Runtime) inspectDockerImage(ctx context.Context, ref string) (*dockertypes.ImageInspect, error) {
	inspect, _, err := r.dockerClient.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &inspect, nil
}

func normalizeDockerImageRef(ref string) string {
	if strings.HasPrefix(ref, "sha256:") && !strings.Contains(ref, "/") && !strings.Contains(ref, "@") {
		return ""
	}
	named, err := refdocker.ParseNormalizedNamed(ref)
	if err != nil {
		return ""
	}
	return refdocker.TagNameOnly(named).String()
}

// ---------------------------------------------------------------------------
// PodRuntime — not applicable for Docker
// ---------------------------------------------------------------------------

func (r *Runtime) ListPods(_ context.Context) ([]runtime.Pod, error) {
	// Docker has no native pod concept.
	return nil, nil
}

// Compile-time interface check.
var _ runtime.Runtime = (*Runtime)(nil)
