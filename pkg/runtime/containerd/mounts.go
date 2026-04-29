package containerd

import (
	"context"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func (r *Runtime) resolveContainerMounts(_ context.Context, containerID string, spec *runtimespec.Spec, pid uint32, criMounts *cri.ContainerMountSet) ([]*runtime.Mount, string) {
	specMounts := runtime.SpecToV1Mounts(spec.Mounts)
	liveMounts, rootFSPath := r.readLiveMounts(pid)

	merged := cri.MergeMountSources(criMounts, specMounts, liveMounts)
	if len(merged) == 0 {
		return specMounts, rootFSPath
	}
	return merged, rootFSPath
}

func (r *Runtime) readLiveMounts(pid uint32) ([]*runtime.Mount, string) {
	if pid == 0 || r.mountReader == nil {
		return nil, ""
	}
	mounts, err := r.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil, ""
	}

	v1Mounts := runtime.ModelMountsToV1(mounts)
	rootFS := resolveRootFS(r, mounts)
	return v1Mounts, rootFS
}

// ---------------------------------------------------------------------------
// Conversion between sysinfo.Mount ↔ runtime.Mount
// ---------------------------------------------------------------------------

func resolveRootFS(r *Runtime, mounts []*sysinfo.Mount) string {
	rootMount := r.mountReader.FindRootMount(mounts)
	if rootMount == nil {
		return ""
	}
	if _, upperdir, _ := r.mountReader.ParseOverlayFS(rootMount); upperdir != "" {
		return upperdir
	}
	return rootMount.Source
}
