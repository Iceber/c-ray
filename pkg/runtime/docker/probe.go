package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/system"
	"github.com/icebergu/c-ray/pkg/runtime"
)

// ImageStoreMode classifies the Docker daemon's image storage backend.
type ImageStoreMode int

const (
	ImageStoreModeUnknown    ImageStoreMode = iota
	ImageStoreModeClassic                   // Traditional Docker graphdriver (overlay2, etc.)
	ImageStoreModeContainerd                // containerd snapshotter-based image store
)

func (m ImageStoreMode) String() string {
	switch m {
	case ImageStoreModeClassic:
		return "classic"
	case ImageStoreModeContainerd:
		return "containerd"
	default:
		return "unknown"
	}
}

// ImageBackendType returns the runtime-level backend identifier.
func (m ImageStoreMode) ImageBackendType() runtime.ImageBackendType {
	switch m {
	case ImageStoreModeContainerd:
		return runtime.ImageBackendDockerContainerd
	default:
		return runtime.ImageBackendDockerClassic
	}
}

// daemonInfo caches metadata obtained from Docker /info at Connect time.
type daemonInfo struct {
	DockerRootDir  string
	Driver         string
	DriverStatus   [][2]string
	ServerVersion  string
	ContainerdAddr string
	ContainerdNS   map[string]string // e.g. {"containers": "moby", "plugins": "plugins.moby"}
	CgroupDriver   string
	CgroupVersion  string

	// OCI runtime metadata from Docker daemon info.
	DefaultRuntime string
	Runtimes       map[string]system.RuntimeWithStatus
}

// probeDaemon fetches Docker system info and determines the image store mode.
func (r *Runtime) probeDaemon(ctx context.Context) (*daemonInfo, ImageStoreMode, error) {
	info, err := r.dockerClient.Info(ctx)
	if err != nil {
		return nil, ImageStoreModeUnknown, fmt.Errorf("docker info: %w", err)
	}

	di := convertDaemonInfo(info)
	mode := detectImageStoreMode(di)

	return di, mode, nil
}

func convertDaemonInfo(info system.Info) *daemonInfo {
	di := &daemonInfo{
		DockerRootDir:  info.DockerRootDir,
		Driver:         info.Driver,
		ServerVersion:  info.ServerVersion,
		CgroupDriver:   info.CgroupDriver,
		CgroupVersion:  info.CgroupVersion,
		DefaultRuntime: info.DefaultRuntime,
		Runtimes:       info.Runtimes,
	}

	if len(info.DriverStatus) > 0 {
		di.DriverStatus = make([][2]string, len(info.DriverStatus))
		copy(di.DriverStatus, info.DriverStatus)
	}

	if info.Containerd != nil {
		di.ContainerdAddr = info.Containerd.Address
		di.ContainerdNS = map[string]string{
			"containers": info.Containerd.Namespaces.Containers,
			"plugins":    info.Containerd.Namespaces.Plugins,
		}
	}

	return di
}

// detectImageStoreMode inspects daemon info to classify the image store mode.
//
// Primary signal: DriverStatus containing driver-type = "io.containerd.snapshotter.v1"
// indicates containerd image store is active (Docker Desktop 4.34+ default,
// Docker Engine 29.0+ fresh installs).
//
// Fallback: classic graphdriver names (overlay2, btrfs, zfs, vfs, etc.)
func detectImageStoreMode(di *daemonInfo) ImageStoreMode {
	if di == nil {
		return ImageStoreModeUnknown
	}

	// Check DriverStatus for the containerd snapshotter marker.
	for _, pair := range di.DriverStatus {
		if pair[0] == "driver-type" && strings.Contains(pair[1], "io.containerd.snapshotter") {
			return ImageStoreModeContainerd
		}
	}

	// Known classic graphdriver names.
	switch di.Driver {
	case "overlay2", "overlay", "btrfs", "zfs", "vfs", "devicemapper", "aufs":
		return ImageStoreModeClassic
	}

	// Known containerd snapshotter names used as Driver field.
	if strings.Contains(di.Driver, "overlayfs") ||
		strings.Contains(di.Driver, "snapshotter") {
		return ImageStoreModeContainerd
	}

	return ImageStoreModeUnknown
}
