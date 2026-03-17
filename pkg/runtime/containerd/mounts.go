package containerd

import (
	"context"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func (r *Runtime) resolveContainerMounts(_ context.Context, containerID string, spec *runtimespec.Spec, pid uint32, criMounts *cri.ContainerMountSet) ([]*runtime.Mount, string) {
	specMounts := specToV1Mounts(spec.Mounts)
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

	v1Mounts := modelMountsToV1(mounts)
	rootFS := resolveRootFS(r, mounts)
	return v1Mounts, rootFS
}

func (r *Runtime) resolveRootFSPath(pid uint32) string {
	if pid == 0 || r.mountReader == nil {
		return ""
	}
	mounts, err := r.mountReader.ReadMounts(int(pid))
	if err != nil {
		return ""
	}
	return resolveRootFS(r, mounts)
}

// ---------------------------------------------------------------------------
// Conversion between models.Mount (sysinfo) ↔ runtime.Mount
// ---------------------------------------------------------------------------

func specToV1Mounts(specMounts []runtimespec.Mount) []*runtime.Mount {
	if len(specMounts) == 0 {
		return nil
	}
	out := make([]*runtime.Mount, 0, len(specMounts))
	for _, m := range specMounts {
		out = append(out, &runtime.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}
	return out
}

func modelMountsToV1(mounts []*models.Mount) []*runtime.Mount {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]*runtime.Mount, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, &runtime.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}
	return out
}

func resolveRootFS(r *Runtime, mounts []*models.Mount) string {
	rootMount := r.mountReader.FindRootMount(mounts)
	if rootMount == nil {
		return ""
	}
	if _, upperdir, _ := r.mountReader.ParseOverlayFS(rootMount); upperdir != "" {
		return upperdir
	}
	return rootMount.Source
}
