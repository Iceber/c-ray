package docker

import (
	"encoding/json"
	"os"
	"path/filepath"

	dockertypes "github.com/docker/docker/api/types"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/icebergu/c-ray/pkg/runtime"
	containerdrt "github.com/icebergu/c-ray/pkg/runtime/containerd"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
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
			State:       runtime.MountStateDeclaredOnly,
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

func (h *containerHandle) readLiveMounts() []*runtime.Mount {
	pid := h.liveContainerPID()
	if pid == 0 || h.rt.mountReader == nil {
		return nil
	}
	mounts, err := h.rt.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil
	}
	return runtime.ModelMountsToV1(mounts)
}

// readSpecMounts loads the OCI runtime spec mounts from the container's
// containerd shim bundle (<bundleDir>/config.json). Spec mounts authoritatively
// describe what runc / dockerd injects (procfs, sysfs, cgroups, /etc/hosts,
// /etc/resolv.conf, ...) in addition to user-declared binds.
func (h *containerHandle) readSpecMounts() []*runtime.Mount {
	if h.inspect == nil || h.rt == nil {
		return nil
	}
	stateDir := dockerContainerdStateDir(h.rt.daemonInfo)
	if stateDir == "" {
		return nil
	}
	bundleDir := containerdrt.ShimBundleDir(
		stateDir, dockerContainerdNamespace(h.rt.daemonInfo), h.inspect.ID,
	)
	data, err := os.ReadFile(filepath.Join(bundleDir, "config.json"))
	if err != nil {
		return nil
	}
	var spec runtimespec.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil
	}
	return runtime.SpecToV1Mounts(spec.Mounts)
}

// mergeDockerMounts produces a unified mount list from three layered sources:
//
//   - declared: user-facing entries from `docker inspect.Mounts`.
//   - spec:     OCI runtime spec mounts from the bundle's config.json. Whatever
//     spec declares but `docker inspect` does not is a runtime default
//     injected by runc / dockerd.
//   - live:     residual /proc/<pid>/mountinfo entries not covered above
//     (typically external hooks like nvidia-container-runtime).
func mergeDockerMounts(declared, spec, live []*runtime.Mount) []*runtime.Mount {
	out := make([]*runtime.Mount, 0, len(declared)+len(spec)+len(live))
	specUsed := make([]bool, len(spec))
	liveUsed := make([]bool, len(live))

	for _, dm := range declared {
		if dm == nil {
			continue
		}
		m := cri.CloneMount(dm)
		if _, idx := cri.FindV1Mount(spec, specUsed, dm.Destination); idx >= 0 {
			specUsed[idx] = true
		}
		if lm, idx := cri.FindV1Mount(live, liveUsed, dm.Destination); idx >= 0 {
			liveUsed[idx] = true
			m.LiveSource = lm.Source
			m.State = runtime.MountStateDeclaredLive
			if m.Type == "" {
				m.Type = lm.Type
			}
			if len(m.Options) == 0 {
				m.Options = append([]string(nil), lm.Options...)
			}
		}
		out = append(out, m)
	}

	for i, sm := range spec {
		if specUsed[i] || sm == nil {
			continue
		}
		liveMatch, li := cri.FindV1Mount(live, liveUsed, sm.Destination)
		if li >= 0 {
			liveUsed[li] = true
		}
		out = append(out, cri.BuildRuntimeDefaultMount(sm, liveMatch))
	}

	for i, lm := range live {
		if liveUsed[i] {
			continue
		}
		out = append(out, cri.BuildLiveExtraMount(lm))
	}

	return out
}
