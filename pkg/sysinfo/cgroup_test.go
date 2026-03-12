package sysinfo

import (
	"testing"
)

func TestDetectCGroupVersion(t *testing.T) {
	version, err := detectCGroupVersion()
	if err != nil {
		t.Skipf("CGroup not available: %v", err)
	}

	if version != CGroupV1 && version != CGroupV2 {
		t.Errorf("Invalid cgroup version: %d", version)
	}

	t.Logf("Detected CGroup version: %d", version)
}

func TestNewCGroupReader(t *testing.T) {
	reader, err := NewCGroupReader()
	if err != nil {
		t.Skipf("CGroup not available: %v", err)
	}

	if reader == nil {
		t.Fatal("CGroupReader is nil")
	}

	version := reader.GetVersion()
	if version != CGroupV1 && version != CGroupV2 {
		t.Errorf("Invalid cgroup version: %d", version)
	}
}

func TestParseMemorySize(t *testing.T) {
	tests := []struct {
		input    string
		expected uint64
	}{
		{"1024 kB", 1024 * 1024},
		{"2048 KB", 2048 * 1024},
		{"512", 512},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseMemorySize(tt.input)
			if err != nil {
				t.Errorf("parseMemorySize(%s) error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("parseMemorySize(%s) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}
