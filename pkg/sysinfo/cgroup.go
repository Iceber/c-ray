package sysinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/icebergu/c-ray/pkg/models"
)

// CGroupVersion represents the cgroup version
type CGroupVersion int

const (
	CGroupV1 CGroupVersion = 1
	CGroupV2 CGroupVersion = 2
)

// CGroupReader reads cgroup information
type CGroupReader struct {
	version CGroupVersion
	rootDir string
}

// NewCGroupReader creates a new cgroup reader
func NewCGroupReader() (*CGroupReader, error) {
	version, err := detectCGroupVersion()
	if err != nil {
		return nil, err
	}

	rootDir := "/sys/fs/cgroup"
	return &CGroupReader{
		version: version,
		rootDir: rootDir,
	}, nil
}

// detectCGroupVersion detects whether the system uses cgroup v1 or v2
func detectCGroupVersion() (CGroupVersion, error) {
	// Check if cgroup v2 is mounted by looking for cgroup.controllers file
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return CGroupV2, nil
	}

	// Check if cgroup v1 is mounted
	if _, err := os.Stat("/sys/fs/cgroup/cpu"); err == nil {
		return CGroupV1, nil
	}

	return 0, fmt.Errorf("unable to detect cgroup version")
}

// GetVersion returns the cgroup version
func (r *CGroupReader) GetVersion() CGroupVersion {
	return r.version
}

// ReadCGroupLimits reads cgroup limits for a given cgroup path
func (r *CGroupReader) ReadCGroupLimits(cgroupPath string) (*models.CGroupLimits, error) {
	if r.version == CGroupV2 {
		return r.readCGroupV2Limits(cgroupPath)
	}
	return r.readCGroupV1Limits(cgroupPath)
}

// readCGroupV1Limits reads cgroup v1 limits
func (r *CGroupReader) readCGroupV1Limits(cgroupPath string) (*models.CGroupLimits, error) {
	limits := &models.CGroupLimits{}

	// Remove leading slash if present
	cgroupPath = strings.TrimPrefix(cgroupPath, "/")

	// Read CPU limits
	cpuQuota, err := r.readInt64(filepath.Join(r.rootDir, "cpu", cgroupPath, "cpu.cfs_quota_us"))
	if err == nil {
		limits.CPUQuota = cpuQuota
	}

	cpuPeriod, err := r.readInt64(filepath.Join(r.rootDir, "cpu", cgroupPath, "cpu.cfs_period_us"))
	if err == nil {
		limits.CPUPeriod = cpuPeriod
	}

	cpuShares, err := r.readInt64(filepath.Join(r.rootDir, "cpu", cgroupPath, "cpu.shares"))
	if err == nil {
		limits.CPUShares = cpuShares
	}

	// Read memory limits
	memLimit, err := r.readInt64(filepath.Join(r.rootDir, "memory", cgroupPath, "memory.limit_in_bytes"))
	if err == nil && memLimit != 9223372036854771712 { // max value means unlimited
		limits.MemoryLimit = memLimit
	}

	memUsage, err := r.readInt64(filepath.Join(r.rootDir, "memory", cgroupPath, "memory.usage_in_bytes"))
	if err == nil {
		limits.MemoryUsage = memUsage
	}

	// Read PIDs limits
	pidsMax, err := r.readInt64(filepath.Join(r.rootDir, "pids", cgroupPath, "pids.max"))
	if err == nil && pidsMax > 0 {
		limits.PidsLimit = pidsMax
	}

	pidsCurrent, err := r.readInt64(filepath.Join(r.rootDir, "pids", cgroupPath, "pids.current"))
	if err == nil {
		limits.PidsCurrent = pidsCurrent
	}

	// Read Block I/O weight
	blkioWeight, err := r.readUint16(filepath.Join(r.rootDir, "blkio", cgroupPath, "blkio.weight"))
	if err == nil {
		limits.BlkioWeight = blkioWeight
	}

	return limits, nil
}

// readCGroupV2Limits reads cgroup v2 limits
func (r *CGroupReader) readCGroupV2Limits(cgroupPath string) (*models.CGroupLimits, error) {
	limits := &models.CGroupLimits{}

	// Remove leading slash if present
	cgroupPath = strings.TrimPrefix(cgroupPath, "/")
	basePath := filepath.Join(r.rootDir, cgroupPath)

	// Read CPU limits from cpu.max (format: "$MAX $PERIOD")
	cpuMax, err := r.readFile(filepath.Join(basePath, "cpu.max"))
	if err == nil {
		parts := strings.Fields(cpuMax)
		if len(parts) >= 2 {
			if parts[0] != "max" {
				if quota, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
					limits.CPUQuota = quota
				}
			}
			if period, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				limits.CPUPeriod = period
			}
		}
	}

	// Read CPU weight (equivalent to shares)
	cpuWeight, err := r.readInt64(filepath.Join(basePath, "cpu.weight"))
	if err == nil {
		limits.CPUShares = cpuWeight
	}

	// Read memory limits
	memMax, err := r.readFile(filepath.Join(basePath, "memory.max"))
	if err == nil && memMax != "max" {
		if limit, err := strconv.ParseInt(strings.TrimSpace(memMax), 10, 64); err == nil {
			limits.MemoryLimit = limit
		}
	}

	memCurrent, err := r.readInt64(filepath.Join(basePath, "memory.current"))
	if err == nil {
		limits.MemoryUsage = memCurrent
	}

	// Read PIDs limits
	pidsMax, err := r.readFile(filepath.Join(basePath, "pids.max"))
	if err == nil && pidsMax != "max" {
		if limit, err := strconv.ParseInt(strings.TrimSpace(pidsMax), 10, 64); err == nil {
			limits.PidsLimit = limit
		}
	}

	pidsCurrent, err := r.readInt64(filepath.Join(basePath, "pids.current"))
	if err == nil {
		limits.PidsCurrent = pidsCurrent
	}

	// Read Block I/O weight
	ioWeight, err := r.readFile(filepath.Join(basePath, "io.weight"))
	if err == nil {
		// Format: "default 100"
		parts := strings.Fields(ioWeight)
		if len(parts) >= 2 {
			if weight, err := strconv.ParseUint(parts[1], 10, 16); err == nil {
				limits.BlkioWeight = uint16(weight)
			}
		}
	}

	return limits, nil
}

// readInt64 reads an int64 value from a file
func (r *CGroupReader) readInt64(path string) (int64, error) {
	content, err := r.readFile(path)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseInt(strings.TrimSpace(content), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse int64 from %s: %w", path, err)
	}

	return value, nil
}

// readUint16 reads a uint16 value from a file
func (r *CGroupReader) readUint16(path string) (uint16, error) {
	content, err := r.readFile(path)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseUint(strings.TrimSpace(content), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("failed to parse uint16 from %s: %w", path, err)
	}

	return uint16(value), nil
}

// readFile reads the content of a file
func (r *CGroupReader) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
