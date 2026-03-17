package crio

import (
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// resolveContainerMounts merges CRI mounts, OCI spec mounts and live procfs
// mounts using the shared v1/cri merge logic.
func resolveContainerMounts(rt *Runtime, spec *runtimespec.Spec, pid uint32, criMounts *cri.ContainerMountSet) ([]*runtime.Mount, error) {
	specMounts := specToV1Mounts(spec)
	liveMounts := readLiveMounts(rt, pid)

	merged := cri.MergeMountSources(criMounts, specMounts, liveMounts)
	if len(merged) == 0 {
		return specMounts, nil
	}
	return merged, nil
}

func specToV1Mounts(spec *runtimespec.Spec) []*runtime.Mount {
	if spec == nil {
		return nil
	}
	out := make([]*runtime.Mount, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		out = append(out, &runtime.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}
	return out
}

func readLiveMounts(rt *Runtime, pid uint32) []*runtime.Mount {
	if pid == 0 || rt.mountReader == nil {
		return nil
	}
	mounts, err := rt.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil
	}
	return modelMountsToV1(mounts)
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
