package sysinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNewProcReader(t *testing.T) {
	reader := NewProcReader()
	if reader == nil {
		t.Fatal("ProcReader is nil")
	}

	if reader.procRoot != "/proc" {
		t.Errorf("Expected procRoot to be /proc, got %s", reader.procRoot)
	}
}

func TestNewProcReaderWithRoot(t *testing.T) {
	customRoot := "/custom/proc"
	reader := NewProcReaderWithRoot(customRoot)
	if reader == nil {
		t.Fatal("ProcReader is nil")
	}

	if reader.procRoot != customRoot {
		t.Errorf("Expected procRoot to be %s, got %s", customRoot, reader.procRoot)
	}
}

func TestListPIDs(t *testing.T) {
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test - /proc not available (not Linux)")
	}

	reader := NewProcReader()
	pids, err := reader.ListPIDs()
	if err != nil {
		t.Fatalf("ListPIDs() error: %v", err)
	}
	if len(pids) == 0 {
		t.Error("Expected at least one PID")
	}

	t.Logf("Found %d PIDs", len(pids))
}
func TestReadCmdlineRaw(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("/usr/bin/shim\x00-start\x00-id\x00abc\x00"), 0o644); err != nil {
		t.Fatalf("failed to write cmdline file: %v", err)
	}

	reader := NewProcReaderWithRoot(procRoot)
	args, err := reader.ReadCmdlineRaw(123)
	if err != nil {
		t.Fatalf("ReadCmdlineRaw() error: %v", err)
	}
	if len(args) != 4 {
		t.Fatalf("ReadCmdlineRaw() len = %d, want 4", len(args))
	}
}

func TestReadExePathAndCwd(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}

	exeTarget := "/usr/bin/containerd-shim-runc-v2"
	if err := os.Symlink(exeTarget, filepath.Join(pidDir, "exe")); err != nil {
		t.Fatalf("failed to create exe symlink: %v", err)
	}
	cwdTarget := "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test"
	if err := os.Symlink(cwdTarget, filepath.Join(pidDir, "cwd")); err != nil {
		t.Fatalf("failed to create cwd symlink: %v", err)
	}

	reader := NewProcReaderWithRoot(procRoot)
	exePath, err := reader.ReadExePath(123)
	if err != nil {
		t.Fatalf("ReadExePath() error: %v", err)
	}
	if exePath != exeTarget {
		t.Fatalf("ReadExePath() = %s, want %s", exePath, exeTarget)
	}
	cwd, err := reader.ReadCwd(123)
	if err != nil {
		t.Fatalf("ReadCwd() error: %v", err)
	}
	if cwd != cwdTarget {
		t.Fatalf("ReadCwd() = %s, want %s", cwd, cwdTarget)
	}
}

func TestReadUnifiedCGroupPath(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}
	content := "0::/kubepods.slice/pod123/container456\n"
	if err := os.WriteFile(filepath.Join(pidDir, "cgroup"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write cgroup file: %v", err)
	}

	reader := NewProcReaderWithRoot(procRoot)
	path, err := reader.ReadUnifiedCGroupPath(123)
	if err != nil {
		t.Fatalf("ReadUnifiedCGroupPath() error: %v", err)
	}
	if path != "/kubepods.slice/pod123/container456" {
		t.Fatalf("ReadUnifiedCGroupPath() = %s", path)
	}
}

func TestReadProcess(t *testing.T) {
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test - /proc not available (not Linux)")
	}

	reader := NewProcReader()

	// Read process 1 (init/systemd)
	process, err := reader.ReadProcess(1)
	if err != nil {
		t.Skipf("Cannot read process 1: %v", err)
	}

	if process.PID != 1 {
		t.Errorf("Expected PID 1, got %d", process.PID)
	}

	if process.Command == "" {
		t.Error("Expected command to be set")
	}

	t.Logf("Process 1: %s (PPID: %d, State: %s)", process.Command, process.PPID, process.State)
}

func TestReadProcessSelf(t *testing.T) {
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test - /proc not available (not Linux)")
	}

	reader := NewProcReader()

	// Read current process
	pid := os.Getpid()
	process, err := reader.ReadProcess(pid)
	if err != nil {
		t.Fatalf("ReadProcess(%d) error: %v", pid, err)
	}

	if process.PID != pid {
		t.Errorf("Expected PID %d, got %d", pid, process.PID)
	}

	t.Logf("Self process: PID=%d, Command=%s, PPID=%d", process.PID, process.Command, process.PPID)
}

func TestGetProcessPPID(t *testing.T) {
	procRoot := t.TempDir()
	writeProcStat(t, procRoot, 123, "shim", "S", 456)

	reader := NewProcReaderWithRoot(procRoot)
	ppid, err := reader.GetProcessPPID(123)
	if err != nil {
		t.Fatalf("GetProcessPPID() error: %v", err)
	}

	if ppid != 456 {
		t.Fatalf("GetProcessPPID() = %d, want 456", ppid)
	}
}

func TestGetProcessPPIDReadError(t *testing.T) {
	reader := NewProcReaderWithRoot(t.TempDir())

	_, err := reader.GetProcessPPID(123)
	if err == nil {
		t.Fatal("GetProcessPPID() expected error, got nil")
	}
}

func writeProcStat(t *testing.T, procRoot string, pid int, command string, state string, ppid int) {
	t.Helper()

	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}

	statContent := strconv.Itoa(pid) + " (" + command + ") " + state + " " + strconv.Itoa(ppid) + " 0 0 0 0 0 0 0 0 0 0 10 20 0 0 0 0 0\n"
	statPath := filepath.Join(pidDir, "stat")
	if err := os.WriteFile(statPath, []byte(statContent), 0o644); err != nil {
		t.Fatalf("failed to write stat file: %v", err)
	}
}
