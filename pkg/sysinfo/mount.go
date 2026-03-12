package sysinfo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/icebergu/c-ray/pkg/models"
)

// MountInfo represents a mount entry from /proc/[pid]/mountinfo
type MountInfo struct {
	MountID        int
	ParentID       int
	Major          int
	Minor          int
	Root           string
	MountPoint     string
	MountOptions   []string
	OptionalFields []string
	FSType         string
	MountSource    string
	SuperOptions   []string
}

// MountReader reads mount information
type MountReader struct{}

// NewMountReader creates a new mount reader
func NewMountReader() *MountReader {
	return &MountReader{}
}

// ReadMounts reads mount information for a given PID
func (r *MountReader) ReadMounts(pid int) ([]*models.Mount, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "mountinfo")
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open mountinfo: %w", err)
	}
	defer file.Close()

	mounts := make([]*models.Mount, 0)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		mountInfo, err := r.parseMountInfoLine(line)
		if err != nil {
			continue // Skip invalid lines
		}

		mount := &models.Mount{
			Source:      mountInfo.MountSource,
			Destination: mountInfo.MountPoint,
			Type:        mountInfo.FSType,
			Options:     mountInfo.MountOptions,
		}

		mounts = append(mounts, mount)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading mountinfo: %w", err)
	}

	return mounts, nil
}

// parseMountInfoLine parses a single line from /proc/[pid]/mountinfo
// Format: 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
func (r *MountReader) parseMountInfoLine(line string) (*MountInfo, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, fmt.Errorf("invalid mountinfo line")
	}

	info := &MountInfo{}

	// Parse fixed fields
	var err error
	info.MountID, err = strconv.Atoi(fields[0])
	if err != nil {
		return nil, err
	}

	info.ParentID, err = strconv.Atoi(fields[1])
	if err != nil {
		return nil, err
	}

	// Parse major:minor
	devParts := strings.Split(fields[2], ":")
	if len(devParts) == 2 {
		info.Major, _ = strconv.Atoi(devParts[0])
		info.Minor, _ = strconv.Atoi(devParts[1])
	}

	info.Root = fields[3]
	info.MountPoint = fields[4]
	info.MountOptions = strings.Split(fields[5], ",")

	// Find the separator "-"
	separatorIdx := -1
	for i := 6; i < len(fields); i++ {
		if fields[i] == "-" {
			separatorIdx = i
			break
		}
	}

	if separatorIdx == -1 {
		return nil, fmt.Errorf("separator not found")
	}

	// Optional fields (between mount options and separator)
	if separatorIdx > 6 {
		info.OptionalFields = fields[6:separatorIdx]
	}

	// Fields after separator
	if separatorIdx+3 < len(fields) {
		info.FSType = fields[separatorIdx+1]
		info.MountSource = fields[separatorIdx+2]
		info.SuperOptions = strings.Split(fields[separatorIdx+3], ",")
	}

	return info, nil
}

// ParseOverlayFS parses overlayfs mount options to extract layer information
func (r *MountReader) ParseOverlayFS(mount *models.Mount) (lowerdir, upperdir, workdir string) {
	if mount.Type != "overlay" && mount.Type != "overlayfs" {
		return
	}

	// Parse options to find lowerdir, upperdir, workdir
	for _, opt := range mount.Options {
		parts := strings.SplitN(opt, "=", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "lowerdir":
			lowerdir = parts[1]
		case "upperdir":
			upperdir = parts[1]
		case "workdir":
			workdir = parts[1]
		}
	}

	return
}

// GetOverlayLayers returns the layers of an overlayfs mount
func (r *MountReader) GetOverlayLayers(mount *models.Mount) []string {
	lowerdir, _, _ := r.ParseOverlayFS(mount)
	if lowerdir == "" {
		return nil
	}

	// lowerdir can contain multiple layers separated by ":"
	layers := strings.Split(lowerdir, ":")
	return layers
}

// FindRootMount finds the root mount for a container
func (r *MountReader) FindRootMount(mounts []*models.Mount) *models.Mount {
	for _, mount := range mounts {
		if mount.Destination == "/" {
			return mount
		}
	}
	return nil
}

// FilterMountsByType filters mounts by filesystem type
func (r *MountReader) FilterMountsByType(mounts []*models.Mount, fsType string) []*models.Mount {
	filtered := make([]*models.Mount, 0)
	for _, mount := range mounts {
		if mount.Type == fsType {
			filtered = append(filtered, mount)
		}
	}
	return filtered
}
