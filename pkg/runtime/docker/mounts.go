package docker

import (
	"sort"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

// convertDockerMounts converts Docker MountPoint entries to runtime.Mount.
func convertDockerMounts(mounts []dockertypes.MountPoint) []*runtime.Mount {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]*runtime.Mount, 0, len(mounts))
	for _, m := range mounts {
		rm := &runtime.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        string(m.Type),
			HostPath:    m.Source,
			Origin:      runtime.MountOriginUser,
			State:       runtime.MountStateDeclaredLive,
		}
		if !m.RW {
			rm.Options = append(rm.Options, "ro")
		}
		if m.Mode != "" {
			rm.Options = append(rm.Options, m.Mode)
		}
		if string(m.Propagation) != "" {
			rm.Options = append(rm.Options, string(m.Propagation))
		}
		out = append(out, rm)
	}
	return out
}

// mergeDockerMounts merges docker-declared mounts with live procfs mounts.
// Docker-declared mounts keep MountOriginUser; unmatched live mounts that
// hit RuntimeDefaultMountTargets get MountOriginRuntimeDefault; the rest
// get MountOriginLiveExtra.
func mergeDockerMounts(declared []*runtime.Mount, reader *sysinfo.MountReader, pid uint32) []*runtime.Mount {
	if pid == 0 || reader == nil {
		return declared
	}
	sysMounts, err := reader.ReadMounts(int(pid))
	if err != nil {
		return declared
	}
	liveMounts := runtime.ModelMountsToV1(sysMounts)

	// Mark live matches on declared mounts.
	liveUsed := make([]bool, len(liveMounts))
	for _, dm := range declared {
		_, li := cri.FindV1Mount(liveMounts, liveUsed, dm.Destination)
		if li >= 0 {
			liveUsed[li] = true
		}
	}

	result := make([]*runtime.Mount, 0, len(declared)+len(liveMounts))
	result = append(result, declared...)

	for i, lm := range liveMounts {
		if liveUsed[i] {
			continue
		}
		if _, ok := cri.RuntimeDefaultMountTargets[lm.Destination]; ok {
			result = append(result, cri.BuildRuntimeDefaultMount(lm, lm))
		} else {
			result = append(result, cri.BuildLiveExtraMount(lm))
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Destination < result[j].Destination
	})
	return result
}
