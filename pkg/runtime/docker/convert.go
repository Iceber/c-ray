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
	case "running", "restarting":
		return runtime.ContainerStatusRunning
	case "paused":
		return runtime.ContainerStatusPaused
	case "exited", "dead", "removing":
		return runtime.ContainerStatusStopped
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

// parseDockerEnv converts Docker-style "KEY=VALUE" env strings.
func parseDockerEnv(env []string) []runtime.EnvVar {
	return runtime.ParseEnvVars(env)
}

// inferDockerCGroupDriver guesses the cgroup driver from the cgroup parent path.
func inferDockerCGroupDriver(cgroupParent string) string {
	return runtime.InferCGroupDriver(cgroupParent)
}

// convertNetworkStats converts models.NetworkStats to runtime.NetworkStats.
var convertNetworkStats = runtime.ConvertNetworkStats

// dirDiskUsage walks a directory and returns total size and inode count.
var dirDiskUsage = runtime.DirDiskUsage
