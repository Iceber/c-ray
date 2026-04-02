package crio

import (
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// resolveContainerMounts merges CRI mounts, OCI spec mounts and live procfs
// mounts using the shared v1/cri merge logic.
func resolveContainerMounts(rt *Runtime, spec *runtimespec.Spec, pid uint32, criMounts *cri.ContainerMountSet) ([]*runtime.Mount, error) {
	var specMounts []*runtime.Mount
	if spec != nil {
		specMounts = runtime.SpecToV1Mounts(spec.Mounts)
	}
	liveMounts := readLiveMounts(rt, pid)

	merged := cri.MergeMountSources(criMounts, specMounts, liveMounts)
	if len(merged) == 0 {
		return specMounts, nil
	}
	return merged, nil
}

func readLiveMounts(rt *Runtime, pid uint32) []*runtime.Mount {
	if pid == 0 || rt.mountReader == nil {
		return nil
	}
	mounts, err := rt.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil
	}
	return runtime.ModelMountsToV1(mounts)
}
