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

// ProcReader reads process information from /proc
type ProcReader struct {
	procRoot string
}

// NewProcReader creates a new proc reader
func NewProcReader() *ProcReader {
	return &ProcReader{
		procRoot: "/proc",
	}
}

// NewProcReaderWithRoot creates a proc reader with custom root
// Useful for reading container processes via /proc/[pid]/root/proc
func NewProcReaderWithRoot(root string) *ProcReader {
	return &ProcReader{
		procRoot: root,
	}
}

// ReadProcess reads process information for a given PID
func (r *ProcReader) ReadProcess(pid int) (*models.Process, error) {
	process := &models.Process{
		PID: pid,
	}

	// Read /proc/[pid]/stat
	if err := r.readStat(pid, process); err != nil {
		return nil, err
	}

	// Read /proc/[pid]/cmdline
	if err := r.readCmdline(pid, process); err != nil {
		// Non-fatal, some processes may not have cmdline
		process.Command = fmt.Sprintf("[%d]", pid)
	}

	// Read /proc/[pid]/status
	if err := r.readStatus(pid, process); err != nil {
		// Non-fatal
	}

	// Read /proc/[pid]/io
	if err := r.readIO(pid, process); err != nil {
		// Non-fatal, may not have permission
	}

	return process, nil
}

// GetProcessPPID reads the parent PID for a given process.
func (r *ProcReader) GetProcessPPID(pid int) (int, error) {
	statFields, err := r.readStatFields(pid)
	if err != nil {
		return 0, err
	}

	return statFields.ppid, nil
}

// ReadCmdlineRaw reads the raw argv vector for a process.
func (r *ProcReader) ReadCmdlineRaw(pid int) ([]string, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "cmdline")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(string(data), "\x00")
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		args = append(args, part)
	}

	if len(args) == 0 {
		return nil, fmt.Errorf("empty cmdline")
	}

	return args, nil
}

// ReadExePath reads the /proc/[pid]/exe symlink target.
func (r *ProcReader) ReadExePath(pid int) (string, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "exe")
	return os.Readlink(path)
}

// ReadCwd reads the /proc/[pid]/cwd symlink target.
func (r *ProcReader) ReadCwd(pid int) (string, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "cwd")
	return os.Readlink(path)
}

// ReadUnifiedCGroupPath reads the cgroup v2 path from /proc/[pid]/cgroup.
func (r *ProcReader) ReadUnifiedCGroupPath(pid int) (string, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "cgroup")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return strings.TrimSpace(parts[2]), nil
		}
	}

	return "", fmt.Errorf("unified cgroup path not found")
}

// readStat reads /proc/[pid]/stat
func (r *ProcReader) readStat(pid int, process *models.Process) error {
	statFields, err := r.readStatFields(pid)
	if err != nil {
		return err
	}

	process.State = statFields.state
	process.PPID = statFields.ppid
	process.UTime = statFields.utime
	process.STime = statFields.stime

	return nil
}

type procStatFields struct {
	state string
	ppid  int
	utime uint64
	stime uint64
}

func (r *ProcReader) readStatFields(pid int) (*procStatFields, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read stat: %w", err)
	}

	return parseProcStat(string(data))
}

func parseProcStat(content string) (*procStatFields, error) {
	// Format: pid (comm) state ppid pgrp session tty_nr tpgid flags
	//         minflt cminflt majflt cmajflt utime stime ...
	// Fields after ')': index 0=state, 1=ppid, ..., 11=utime, 12=stime
	startIdx := strings.Index(content, "(")
	endIdx := strings.LastIndex(content, ")")
	if startIdx == -1 || endIdx == -1 {
		return nil, fmt.Errorf("invalid stat format")
	}

	fields := strings.Fields(content[endIdx+2:])
	if len(fields) < 13 {
		return nil, fmt.Errorf("insufficient fields in stat")
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("invalid ppid in stat: %w", err)
	}

	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid utime in stat: %w", err)
	}

	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid stime in stat: %w", err)
	}

	return &procStatFields{
		state: fields[0],
		ppid:  ppid,
		utime: utime,
		stime: stime,
	}, nil
}

// readCmdline reads /proc/[pid]/cmdline
func (r *ProcReader) readCmdline(pid int, process *models.Process) error {
	args, err := r.ReadCmdlineRaw(pid)
	if err != nil {
		return err
	}

	process.Command = filepath.Base(args[0])
	if len(args) > 1 {
		process.Args = args[1:]
	}

	return nil
}

// readStatus reads /proc/[pid]/status
func (r *ProcReader) readStatus(pid int, process *models.Process) error {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "status")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "VmRSS":
			// Format: "12345 kB"
			if size, err := parseMemorySize(value); err == nil {
				process.MemoryRSS = size
			}
		case "VmSize":
			if size, err := parseMemorySize(value); err == nil {
				process.MemoryVMS = size
			}
		}
	}

	return scanner.Err()
}

// readIO reads /proc/[pid]/io
func (r *ProcReader) readIO(pid int, process *models.Process) error {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "io")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "read_bytes":
			if bytes, err := strconv.ParseUint(value, 10, 64); err == nil {
				process.ReadBytes = bytes
			}
		case "write_bytes":
			if bytes, err := strconv.ParseUint(value, 10, 64); err == nil {
				process.WriteBytes = bytes
			}
		case "syscr":
			if ops, err := strconv.ParseUint(value, 10, 64); err == nil {
				process.ReadOps = ops
			}
		case "syscw":
			if ops, err := strconv.ParseUint(value, 10, 64); err == nil {
				process.WriteOps = ops
			}
		}
	}

	return scanner.Err()
}

// ListPIDs returns all PIDs in the proc filesystem
func (r *ProcReader) ListPIDs() ([]int, error) {
	entries, err := os.ReadDir(r.procRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read proc directory: %w", err)
	}

	pids := make([]int, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if directory name is a number (PID)
		if pid, err := strconv.Atoi(entry.Name()); err == nil {
			pids = append(pids, pid)
		}
	}

	return pids, nil
}

// ReadNetDev reads network interface statistics from /proc/[pid]/net/dev
func (r *ProcReader) ReadNetDev(pid int) ([]*models.NetworkStats, error) {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "net", "dev")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var stats []*models.NetworkStats
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		// Skip the first 2 header lines
		if lineNo <= 2 {
			continue
		}

		line := scanner.Text()
		// Format: "  iface: rx_bytes rx_packets rx_errs rx_drop rx_fifo rx_frame rx_compressed rx_multicast tx_bytes tx_packets tx_errs tx_drop tx_fifo tx_colls tx_carrier tx_compressed"
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if r.skipNetInterface(pid, iface) {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}

		ns := &models.NetworkStats{Interface: iface}
		ns.RxBytes, _ = strconv.ParseUint(fields[0], 10, 64)
		ns.RxPackets, _ = strconv.ParseUint(fields[1], 10, 64)
		ns.RxErrors, _ = strconv.ParseUint(fields[2], 10, 64)
		ns.RxDropped, _ = strconv.ParseUint(fields[3], 10, 64)
		ns.TxBytes, _ = strconv.ParseUint(fields[8], 10, 64)
		ns.TxPackets, _ = strconv.ParseUint(fields[9], 10, 64)
		ns.TxErrors, _ = strconv.ParseUint(fields[10], 10, 64)
		ns.TxDropped, _ = strconv.ParseUint(fields[11], 10, 64)

		stats = append(stats, ns)
	}

	return stats, scanner.Err()
}

// skipNetInterface returns true for network interfaces that are not meaningful
// to monitor inside a container context.
//
// Strategy:
//  1. Always apply name-based blacklist (lo, veth*, virbr*, kernel tunnel defaults).
//  2. If sysfs is readable, also reject non-ARPHRD_ETHER (type != 1) interfaces.
//
// Both layers are needed because some kernel default devices (gretap0, erspan0,
// ip6gretap0, ip6erspan0) operate at L2 and report ARPHRD_ETHER, so sysfs
// alone cannot distinguish them from real interfaces.
func (r *ProcReader) skipNetInterface(pid int, name string) bool {
	if name == "lo" {
		return true
	}

	// Name-based blacklist — always checked.
	for _, prefix := range []string{"veth", "virbr"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	// Kernel default tunnel/virtual devices — auto-created by kernel modules.
	// Includes L2 devices (gretap0, erspan0, etc.) that have ARPHRD_ETHER type.
	kernelDefaults := map[string]struct{}{
		"tunl0": {}, "gre0": {}, "gretap0": {}, "erspan0": {},
		"ip_vti0": {}, "ip6_vti0": {}, "ip6tnl0": {},
		"ip6gre0": {}, "ip6gretap0": {}, "ip6erspan0": {},
		"sit0": {},
	}
	if _, skip := kernelDefaults[name]; skip {
		return true
	}

	// sysfs-based detection: /proc/<pid>/root/sys/class/net/<iface>/type
	// Rejects interfaces whose type is not ARPHRD_ETHER (1), e.g. IPIP(768),
	// SIT(776), GRE(778), NONE(65534).
	sysPath := filepath.Join(r.procRoot, strconv.Itoa(pid), "root", "sys", "class", "net", name, "type")
	if data, err := os.ReadFile(sysPath); err == nil {
		ifType := strings.TrimSpace(string(data))
		if ifType != "1" {
			return true
		}
	}

	return false
}

// parseMemorySize parses memory size from status file (e.g., "12345 kB")
func parseMemorySize(s string) (uint64, error) {
	parts := strings.Fields(s)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid memory size format")
	}

	size, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}

	// Convert to bytes (default unit is kB)
	if len(parts) > 1 && strings.ToLower(parts[1]) == "kb" {
		size *= 1024
	}

	return size, nil
}
