package containerd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/icebergu/c-ray/pkg/models"
	runtimecri "github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

var runtimeDefaultMountTargets = map[string]struct{}{
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

func (r *ContainerdRuntime) resolveContainerMounts(ctx context.Context, containerID string, spec *runtimespec.Spec, pid uint32) ([]*models.Mount, string) {
	specMounts := buildMountsFromSpec(spec.Mounts)
	liveMounts, mountRootFSPath := r.readLiveMounts(pid)

	var criMounts *runtimecri.ContainerMounts
	if r.criClient != nil {
		if mounts, err := r.criClient.InspectContainerMounts(ctx, containerID); err == nil {
			criMounts = mounts
		}
	}

	merged := mergeMountSources(criMounts, specMounts, liveMounts)
	if len(merged) == 0 {
		return specMounts, mountRootFSPath
	}

	return merged, mountRootFSPath
}

func (r *ContainerdRuntime) readLiveMounts(pid uint32) ([]*models.Mount, string) {
	if pid == 0 || r.mountReader == nil {
		return nil, ""
	}

	mounts, err := r.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil, ""
	}

	return mounts, r.resolveMountRootFSPath(mounts)
}

func mergeMountSources(criMounts *runtimecri.ContainerMounts, specMounts, liveMounts []*models.Mount) []*models.Mount {
	result := make([]*models.Mount, 0, len(specMounts)+len(liveMounts))
	specUsed := make([]bool, len(specMounts))
	liveUsed := make([]bool, len(liveMounts))
	statusUsed := make([]bool, len(criStatusMounts(criMounts)))

	configMounts := criConfigMounts(criMounts)
	statusMounts := criStatusMounts(criMounts)

	for _, configMount := range configMounts {
		statusMatch, statusIndex := findCRIMount(statusMounts, statusUsed, configMount.ContainerPath)
		if statusIndex >= 0 {
			statusUsed[statusIndex] = true
		}
		specMatch, specIndex := findSpecMount(specMounts, specUsed, configMount.ContainerPath)
		if specIndex >= 0 {
			specUsed[specIndex] = true
		}
		liveMatch, liveIndex := findLiveMount(liveMounts, liveUsed, configMount.ContainerPath)
		if liveIndex >= 0 {
			liveUsed[liveIndex] = true
		}
		result = append(result, buildCRIMount(configMount, statusMatch, specMatch, liveMatch))
	}

	for index, statusMount := range statusMounts {
		if statusUsed[index] {
			continue
		}
		liveMatch, liveIndex := findLiveMount(liveMounts, liveUsed, statusMount.ContainerPath)
		if liveIndex >= 0 {
			liveUsed[liveIndex] = true
		}
		result = append(result, buildCRIStatusOnlyMount(statusMount, liveMatch))
	}

	for index, specMount := range specMounts {
		if specUsed[index] {
			continue
		}
		liveMatch, liveIndex := findLiveMount(liveMounts, liveUsed, specMount.Destination)
		if liveIndex >= 0 {
			liveUsed[liveIndex] = true
		}
		result = append(result, buildRuntimeDefaultMount(specMount, liveMatch))
	}

	for index, liveMount := range liveMounts {
		if liveUsed[index] {
			continue
		}
		result = append(result, buildLiveExtraMount(liveMount))
	}

	sort.SliceStable(result, func(i, j int) bool {
		leftRank := mountOriginRank(result[i].Origin)
		rightRank := mountOriginRank(result[j].Origin)
		if leftRank != rightRank {
			return leftRank < rightRank
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

func criConfigMounts(criMounts *runtimecri.ContainerMounts) []*runtimecri.Mount {
	if criMounts == nil {
		return nil
	}
	return criMounts.ConfigMounts
}

func criStatusMounts(criMounts *runtimecri.ContainerMounts) []*runtimecri.Mount {
	if criMounts == nil {
		return nil
	}
	return criMounts.StatusMounts
}

func findCRIMount(mounts []*runtimecri.Mount, used []bool, destination string) (*runtimecri.Mount, int) {
	for index, mount := range mounts {
		if used[index] || mount == nil {
			continue
		}
		if sameContainerDestination(mount.ContainerPath, destination) {
			return mount, index
		}
	}
	return nil, -1
}

func findSpecMount(mounts []*models.Mount, used []bool, destination string) (*models.Mount, int) {
	for index, mount := range mounts {
		if used[index] || mount == nil {
			continue
		}
		if sameContainerDestination(mount.Destination, destination) {
			return mount, index
		}
	}
	return nil, -1
}

func findLiveMount(mounts []*models.Mount, used []bool, destination string) (*models.Mount, int) {
	for index, mount := range mounts {
		if used[index] || mount == nil {
			continue
		}
		if sameContainerDestination(mount.Destination, destination) {
			return mount, index
		}
	}
	return nil, -1
}

func sameContainerDestination(left, right string) bool {
	left = cleanContainerDestination(left)
	right = cleanContainerDestination(right)
	if left == right {
		return true
	}
	return normalizeRunAlias(left) == normalizeRunAlias(right)
}

func cleanContainerDestination(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func normalizeRunAlias(path string) string {
	path = cleanContainerDestination(path)
	if path == "/var/run" {
		return "/run"
	}
	if strings.HasPrefix(path, "/var/run/") {
		return "/run/" + strings.TrimPrefix(path, "/var/run/")
	}
	return path
}

func buildCRIMount(configMount, statusMount *runtimecri.Mount, specMount, liveMount *models.Mount) *models.Mount {
	hostPath := configMount.HostPath
	if hostPath == "" && statusMount != nil {
		hostPath = statusMount.HostPath
	}
	source := hostPath
	if source == "" && configMount.Image != "" {
		source = configMount.Image
	}
	if source == "" && liveMount != nil {
		source = liveMount.Source
	}

	mount := &models.Mount{
		Source:      source,
		Destination: configMount.ContainerPath,
		Type:        bestMountType(specMount, liveMount, configMount),
		Options:     bestMountOptions(specMount, liveMount, runtimecri.MountOptions(configMount)),
		HostPath:    hostPath,
		Origin:      models.MountOriginCRI,
		LiveSource:  liveMountSource(liveMount),
		State:       declaredState(statusMount != nil || liveMount != nil),
		Note:        criMountNote(configMount, statusMount, liveMount),
	}
	return mount
}

func buildCRIStatusOnlyMount(statusMount *runtimecri.Mount, liveMount *models.Mount) *models.Mount {
	return &models.Mount{
		Source:      firstNonEmpty(statusMount.HostPath, liveMountSource(liveMount)),
		Destination: statusMount.ContainerPath,
		Type:        bestMountType(nil, liveMount, statusMount),
		Options:     bestMountOptions(nil, liveMount, runtimecri.MountOptions(statusMount)),
		HostPath:    statusMount.HostPath,
		Origin:      models.MountOriginCRI,
		LiveSource:  liveMountSource(liveMount),
		State:       models.MountStateLiveOnly,
		Note:        "status-only CRI mount",
	}
}

func buildRuntimeDefaultMount(specMount, liveMount *models.Mount) *models.Mount {
	mount := cloneMount(specMount)
	mount.HostPath = specMount.Source
	mount.Origin = models.MountOriginRuntimeDefault
	mount.LiveSource = liveMountSource(liveMount)
	mount.State = declaredState(liveMount != nil)
	mount.Note = runtimeDefaultMountNote(specMount)
	if liveMount != nil {
		mount.Type = bestMountType(specMount, liveMount, nil)
		mount.Options = bestMountOptions(specMount, liveMount, nil)
	}
	return mount
}

func buildLiveExtraMount(liveMount *models.Mount) *models.Mount {
	mount := cloneMount(liveMount)
	mount.HostPath = ""
	mount.LiveSource = liveMount.Source
	mount.Origin = models.MountOriginLiveExtra
	mount.State = models.MountStateLiveOnly
	mount.Note = "live mountinfo entry outside CRI and spec declarations"
	return mount
}

func cloneMount(mount *models.Mount) *models.Mount {
	if mount == nil {
		return &models.Mount{}
	}
	return &models.Mount{
		Source:      mount.Source,
		Destination: mount.Destination,
		Type:        mount.Type,
		Options:     append([]string(nil), mount.Options...),
		HostPath:    mount.HostPath,
		LiveSource:  mount.LiveSource,
		Origin:      mount.Origin,
		State:       mount.State,
		Note:        mount.Note,
	}
}

func bestMountType(specMount, liveMount *models.Mount, criMount *runtimecri.Mount) string {
	if specMount != nil && specMount.Type != "" {
		return specMount.Type
	}
	if liveMount != nil && liveMount.Type != "" {
		return liveMount.Type
	}
	if criMount != nil && criMount.Image != "" {
		return "image"
	}
	if criMount != nil {
		return "bind"
	}
	return "unknown"
}

func bestMountOptions(specMount, liveMount *models.Mount, criOptions []string) []string {
	if specMount != nil && len(specMount.Options) > 0 {
		return append([]string(nil), specMount.Options...)
	}
	if liveMount != nil && len(liveMount.Options) > 0 {
		return append([]string(nil), liveMount.Options...)
	}
	if len(criOptions) > 0 {
		return append([]string(nil), criOptions...)
	}
	return nil
}

func declaredState(live bool) models.MountState {
	if live {
		return models.MountStateDeclaredLive
	}
	return models.MountStateDeclaredOnly
}

func liveMountSource(mount *models.Mount) string {
	if mount == nil {
		return ""
	}
	return mount.Source
}

func criMountNote(configMount, statusMount *runtimecri.Mount, liveMount *models.Mount) string {
	parts := []string{"CRI external mount"}
	if configMount.Image != "" {
		parts = append(parts, "image-backed")
	}
	if statusMount != nil {
		parts = append(parts, "confirmed by CRI status")
	}
	if liveMount != nil && liveMount.Source != "" && liveMount.Source != configMount.HostPath {
		parts = append(parts, fmt.Sprintf("live source %s", liveMount.Source))
	}
	return strings.Join(parts, "; ")
}

func runtimeDefaultMountNote(mount *models.Mount) string {
	if _, ok := runtimeDefaultMountTargets[mount.Destination]; ok {
		return "runtime default support mount"
	}
	return "spec mount not claimed by CRI"
}

func mountOriginRank(origin models.MountOrigin) int {
	switch origin {
	case models.MountOriginCRI:
		return 0
	case models.MountOriginRuntimeDefault:
		return 1
	case models.MountOriginLiveExtra:
		return 2
	default:
		return 3
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
