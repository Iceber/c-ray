package containerd

import (
	"context"
	"testing"

	"github.com/icebergu/c-ray/pkg/runtime"
)

func TestNewContainerdRuntime(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	if rt == nil {
		t.Fatal("NewContainerdRuntime returned nil")
	}

	if rt.config != config {
		t.Error("Config not set correctly")
	}
}

func TestConvertStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"created", "created"},
		{"running", "running"},
		{"paused", "paused"},
		{"stopped", "stopped"},
		{"unknown", "unknown"},
		{"invalid", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := convertStatus(tt.input)
			if string(result) != tt.expected {
				t.Errorf("convertStatus(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConnect_NotConnected(t *testing.T) {
	t.Skip("Skipping test - requires containerd to be running")

	config := &runtime.Config{
		SocketPath: "/nonexistent/socket.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// This should fail because the socket doesn't exist
	err := rt.Connect(ctx)
	if err == nil {
		t.Error("Expected error when connecting to nonexistent socket")
		rt.Close()
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestClose_NilClient(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)

	// Should not panic when client is nil
	err := rt.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestListContainers_NotConnected(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// Should return error when not connected
	_, err := rt.ListContainers(ctx)
	if err == nil {
		t.Error("Expected error when listing containers without connection")
	}
}

func TestListImages_NotConnected(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// Should return error when not connected
	_, err := rt.ListImages(ctx)
	if err == nil {
		t.Error("Expected error when listing images without connection")
	}
}
