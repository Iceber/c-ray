package sysinfo

import (
	"os"
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
