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

// readStat reads /proc/[pid]/stat
func (r *ProcReader) readStat(pid int, process *models.Process) error {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read stat: %w", err)
	}

	// Parse stat file
	// Format: pid (comm) state ppid ...
	content := string(data)

	// Find the command name (enclosed in parentheses)
	startIdx := strings.Index(content, "(")
	endIdx := strings.LastIndex(content, ")")
	if startIdx == -1 || endIdx == -1 {
		return fmt.Errorf("invalid stat format")
	}

	// Extract fields after the command name
	fields := strings.Fields(content[endIdx+2:])
	if len(fields) < 2 {
		return fmt.Errorf("insufficient fields in stat")
	}

	// Field 0: state
	process.State = fields[0]

	// Field 1: ppid
	if ppid, err := strconv.Atoi(fields[1]); err == nil {
		process.PPID = ppid
	}

	return nil
}

// readCmdline reads /proc/[pid]/cmdline
func (r *ProcReader) readCmdline(pid int, process *models.Process) error {
	path := filepath.Join(r.procRoot, strconv.Itoa(pid), "cmdline")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// cmdline is null-separated
	parts := strings.Split(string(data), "\x00")
	if len(parts) > 0 && parts[0] != "" {
		process.Command = filepath.Base(parts[0])
		if len(parts) > 1 {
			process.Args = parts[1 : len(parts)-1] // Last element is usually empty
		}
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
