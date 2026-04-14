package docker

import (
	dockertypes "github.com/docker/docker/api/types"
	"github.com/icebergu/c-ray/pkg/runtime"
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
			Origin:      runtime.MountOriginKubelet,
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
