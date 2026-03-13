package views

import (
	"testing"

	"github.com/icebergu/c-ray/pkg/models"
)

func TestBuildPIDNamespaceRowsInfersSharedPIDFromNamespacePath(t *testing.T) {
	detail := &models.ContainerDetail{
		Namespaces: map[string]string{"pid": "/proc/12345/ns/pid"},
	}

	rows := buildPIDNamespaceRows(detail)
	if rows[0] != "Shared PID: true" {
		t.Fatalf("Shared PID row = %q", rows[0])
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(rows))
	}
	if got := pidNamespaceSummary(detail); got != "shared" {
		t.Fatalf("pidNamespaceSummary() = %q", got)
	}
}

func TestBuildPIDNamespaceRowsInfersPrivatePIDNamespace(t *testing.T) {
	detail := &models.ContainerDetail{
		Namespaces: map[string]string{"pid": ""},
	}

	rows := buildPIDNamespaceRows(detail)
	if rows[0] != "Shared PID: false" {
		t.Fatalf("Shared PID row = %q", rows[0])
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(rows))
	}
	if got := pidNamespaceSummary(detail); got != "private" {
		t.Fatalf("pidNamespaceSummary() = %q", got)
	}
}
