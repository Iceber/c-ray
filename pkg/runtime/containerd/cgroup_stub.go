//go:build !linux

package containerd

import (
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

// loadCGroupStats is a no-op on non-Linux platforms where cgroup
// filesystems are not available.
func loadCGroupStats(_ *runtime.ContainerCGroupInfo, _ *sysinfo.CGroupReader) {}
