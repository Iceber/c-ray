package docker

import (
	"strings"

	"github.com/icebergu/c-ray/pkg/runtime"
)

// convertDockerStatus maps Docker container state strings to runtime status.
func convertDockerStatus(state string) runtime.ContainerStatus {
	switch strings.ToLower(state) {
	case "created":
		return runtime.ContainerStatusCreated
	case "running":
		return runtime.ContainerStatusRunning
	case "restarting":
		return runtime.ContainerStatusRestarting
	case "pausing":
		return runtime.ContainerStatusPausing
	case "paused":
		return runtime.ContainerStatusPaused
	case "exited", "removing":
		return runtime.ContainerStatusStopped
	case "dead":
		return runtime.ContainerStatusDead
	default:
		return runtime.ContainerStatusUnknown
	}
}

// dockerContainerName extracts a display name from Docker container names.
// Docker prepends "/" to names; a container may have multiple names.
func dockerContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// convertNetworkStats converts sysinfo.NetworkStats to runtime.NetworkStats.
var convertNetworkStats = runtime.ConvertNetworkStats

// dirDiskUsage walks a directory and returns total size and inode count.
var dirDiskUsage = runtime.DirDiskUsage
