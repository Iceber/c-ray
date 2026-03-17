package cri

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/icebergu/c-ray/pkg/runtime"
)

// RuntimeDefaultMountTargets lists destinations commonly injected by the
// container runtime rather than declared via CRI config.
var RuntimeDefaultMountTargets = map[string]struct{}{
	"/proc":            {},
	"/dev":             {},
	"/dev/pts":         {},
	"/dev/shm":         {},
	"/dev/mqueue":      {},
	"/sys":             {},
	"/run":             {},
	"/etc/resolv.conf": {},
	"/sys/fs/cgroup":   {},
}

// MergeMountSources performs a four-phase merge of CRI-declared mounts,
// OCI spec mounts and live procfs mounts into a unified runtime.Mount slice.
//
// Phase 1: CRI config mounts (matched against status, spec and live).
// Phase 2: CRI status-only mounts not covered by config.
// Phase 3: Remaining OCI spec mounts not claimed by CRI.
// Phase 4: Unmatched live mounts.
func MergeMountSources(mountSet *ContainerMountSet, specMounts, liveMounts []*runtime.Mount) []*runtime.Mount {
	var criMounts *ContainerMounts
	if mountSet != nil {
		criMounts = mountSet.raw
	}
	result := make([]*runtime.Mount, 0, len(specMounts)+len(liveMounts))
	specUsed := make([]bool, len(specMounts))
	liveUsed := make([]bool, len(liveMounts))

	configMounts := criConfigMounts(criMounts)
	statusMounts := criStatusMounts(criMounts)
	statusUsed := make([]bool, len(statusMounts))

	// Phase 1: CRI config mounts.
	for _, cm := range configMounts {
		statusMatch, si := findCRIMount(statusMounts, statusUsed, cm.ContainerPath)
		if si >= 0 {
			statusUsed[si] = true
		}
		specMatch, spi := FindV1Mount(specMounts, specUsed, cm.ContainerPath)
		if spi >= 0 {
			specUsed[spi] = true
		}
		liveMatch, li := FindV1Mount(liveMounts, liveUsed, cm.ContainerPath)
		if li >= 0 {
			liveUsed[li] = true
		}
		result = append(result, buildCRIMount(cm, statusMatch, specMatch, liveMatch))
	}

	// Phase 2: CRI status-only mounts.
	for i, sm := range statusMounts {
		if statusUsed[i] {
			continue
		}
		liveMatch, li := FindV1Mount(liveMounts, liveUsed, sm.ContainerPath)
		if li >= 0 {
			liveUsed[li] = true
		}
		result = append(result, buildCRIStatusOnlyMount(sm, liveMatch))
	}

	// Phase 3: Remaining spec mounts.
	for i, sm := range specMounts {
		if specUsed[i] {
			continue
		}
		liveMatch, li := FindV1Mount(liveMounts, liveUsed, sm.Destination)
		if li >= 0 {
			liveUsed[li] = true
		}
		result = append(result, BuildRuntimeDefaultMount(sm, liveMatch))
	}

	// Phase 4: Unmatched live mounts.
	for i, lm := range liveMounts {
		if liveUsed[i] {
			continue
		}
		result = append(result, BuildLiveExtraMount(lm))
	}

	sort.SliceStable(result, func(i, j int) bool {
		ri, rj := mountOriginRank(result[i].Origin), mountOriginRank(result[j].Origin)
		if ri != rj {
			return ri < rj
		}
		if result[i].Destination != result[j].Destination {
			return result[i].Destination < result[j].Destination
		}
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Type < result[j].Type
	})

	return result
}

// ---------------------------------------------------------------------------
// CRI mount helpers
// ---------------------------------------------------------------------------

func criConfigMounts(m *ContainerMounts) []*Mount {
	if m == nil {
		return nil
	}
	return m.ConfigMounts
}

func criStatusMounts(m *ContainerMounts) []*Mount {
	if m == nil {
		return nil
	}
	return m.StatusMounts
}

func findCRIMount(mounts []*Mount, used []bool, dest string) (*Mount, int) {
	for i, m := range mounts {
		if used[i] || m == nil {
			continue
		}
		if SameDestination(m.ContainerPath, dest) {
			return m, i
		}
	}
	return nil, -1
}

// FindV1Mount locates the first unused runtime.Mount whose Destination matches
// dest (accounting for /var/run ↔ /run aliasing). The caller is responsible
// for marking the returned index as used.
func FindV1Mount(mounts []*runtime.Mount, used []bool, dest string) (*runtime.Mount, int) {
	for i, m := range mounts {
		if used[i] || m == nil {
			continue
		}
		if SameDestination(m.Destination, dest) {
			return m, i
		}
	}
	return nil, -1
}

// ---------------------------------------------------------------------------
// Mount builders
// ---------------------------------------------------------------------------

func buildCRIMount(cfg, status *Mount, spec, live *runtime.Mount) *runtime.Mount {
	hostPath := cfg.HostPath
	if hostPath == "" && status != nil {
		hostPath = status.HostPath
	}
	source := hostPath
	if source == "" && cfg.Image != "" {
		source = cfg.Image
	}
	if source == "" && live != nil {
		source = live.Source
	}

	return &runtime.Mount{
		Source:      source,
		Destination: cfg.ContainerPath,
		Type:        bestType(spec, live, cfg),
		Options:     bestOptions(spec, live, MountOptions(cfg)),
		HostPath:    hostPath,
		Origin:      runtime.MountOriginCRI,
		LiveSource:  liveSource(live),
		State:       declaredState(status != nil || live != nil),
		Note:        criMountNote(cfg, status, live),
	}
}

func buildCRIStatusOnlyMount(status *Mount, live *runtime.Mount) *runtime.Mount {
	return &runtime.Mount{
		Source:      FirstNonEmpty(status.HostPath, liveSource(live)),
		Destination: status.ContainerPath,
		Type:        bestType(nil, live, status),
		Options:     bestOptions(nil, live, MountOptions(status)),
		HostPath:    status.HostPath,
		Origin:      runtime.MountOriginCRI,
		LiveSource:  liveSource(live),
		State:       runtime.MountStateLiveOnly,
		Note:        "status-only CRI mount",
	}
}

// BuildRuntimeDefaultMount wraps an OCI-spec mount that was not claimed by
// any CRI config entry with optional live-mount correlation.
func BuildRuntimeDefaultMount(spec, live *runtime.Mount) *runtime.Mount {
	m := CloneMount(spec)
	m.HostPath = spec.Source
	m.Origin = runtime.MountOriginRuntimeDefault
	m.LiveSource = liveSource(live)
	m.State = declaredState(live != nil)
	m.Note = RuntimeDefaultNote(spec)
	if live != nil {
		m.Type = bestType(spec, live, nil)
		m.Options = bestOptions(spec, live, nil)
	}
	return m
}

// BuildLiveExtraMount wraps a live procfs mount that was not declared
// by CRI or OCI spec.
func BuildLiveExtraMount(live *runtime.Mount) *runtime.Mount {
	m := CloneMount(live)
	m.HostPath = ""
	m.LiveSource = live.Source
	m.Origin = runtime.MountOriginLiveExtra
	m.State = runtime.MountStateLiveOnly
	m.Note = "live mountinfo entry outside CRI and spec declarations"
	return m
}

// ---------------------------------------------------------------------------
// Mount utilities
// ---------------------------------------------------------------------------

// CloneMount returns a deep copy of m.
func CloneMount(m *runtime.Mount) *runtime.Mount {
	if m == nil {
		return &runtime.Mount{}
	}
	return &runtime.Mount{
		Source:      m.Source,
		Destination: m.Destination,
		Type:        m.Type,
		Options:     append([]string(nil), m.Options...),
		HostPath:    m.HostPath,
		LiveSource:  m.LiveSource,
		Origin:      m.Origin,
		State:       m.State,
		Note:        m.Note,
	}
}

func bestType(spec, live *runtime.Mount, cri *Mount) string {
	if spec != nil && spec.Type != "" {
		return spec.Type
	}
	if live != nil && live.Type != "" {
		return live.Type
	}
	if cri != nil && cri.Image != "" {
		return "image"
	}
	if cri != nil {
		return "bind"
	}
	return "unknown"
}

func bestOptions(spec, live *runtime.Mount, criOpts []string) []string {
	if spec != nil && len(spec.Options) > 0 {
		return append([]string(nil), spec.Options...)
	}
	if live != nil && len(live.Options) > 0 {
		return append([]string(nil), live.Options...)
	}
	if len(criOpts) > 0 {
		return append([]string(nil), criOpts...)
	}
	return nil
}

func declaredState(live bool) runtime.MountState {
	if live {
		return runtime.MountStateDeclaredLive
	}
	return runtime.MountStateDeclaredOnly
}

func liveSource(m *runtime.Mount) string {
	if m == nil {
		return ""
	}
	return m.Source
}

// SameDestination compares two container mount destinations, normalising
// for /var/run ↔ /run aliasing.
func SameDestination(a, b string) bool {
	a = cleanDest(a)
	b = cleanDest(b)
	if a == b {
		return true
	}
	return normalizeRunAlias(a) == normalizeRunAlias(b)
}

func cleanDest(path string) string {
	if path == "" {
		return ""
	}
	c := filepath.Clean(path)
	if !strings.HasPrefix(c, "/") {
		c = "/" + c
	}
	return c
}

func normalizeRunAlias(path string) string {
	p := cleanDest(path)
	if p == "/var/run" {
		return "/run"
	}
	if strings.HasPrefix(p, "/var/run/") {
		return "/run/" + strings.TrimPrefix(p, "/var/run/")
	}
	return p
}

func criMountNote(cfg, status *Mount, live *runtime.Mount) string {
	parts := []string{"CRI external mount"}
	if cfg.Image != "" {
		parts = append(parts, "image-backed")
	}
	if status != nil {
		parts = append(parts, "confirmed by CRI status")
	}
	if live != nil && live.Source != "" && live.Source != cfg.HostPath {
		parts = append(parts, fmt.Sprintf("live source %s", live.Source))
	}
	return strings.Join(parts, "; ")
}

// RuntimeDefaultNote produces a human-readable annotation for a spec mount
// that was not claimed by CRI.
func RuntimeDefaultNote(m *runtime.Mount) string {
	if _, ok := RuntimeDefaultMountTargets[m.Destination]; ok {
		return "runtime default support mount"
	}
	return "spec mount not claimed by CRI"
}

func mountOriginRank(origin runtime.MountOrigin) int {
	switch origin {
	case runtime.MountOriginCRI:
		return 0
	case runtime.MountOriginRuntimeDefault:
		return 1
	case runtime.MountOriginLiveExtra:
		return 2
	default:
		return 3
	}
}

// FirstNonEmpty returns the first non-empty string from the given values.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
