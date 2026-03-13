package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
)

func TestImageLayersViewToggleBrowserWithI(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "hello.txt"), []byte("hello from layer"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	view := NewImageLayersView(nil, nil, nil)
	view.layers = []*models.ImageLayer{{Index: 3, SnapshotPath: rootDir, SnapshotKey: "sha256:test"}}
	view.render()

	readOnlyGroup := view.tree.GetRoot().GetChildren()[1]
	layerNode := readOnlyGroup.GetChildren()[0]
	view.tree.SetCurrentNode(layerNode)

	if handled := view.HandleInput(tcell.NewEventKey(tcell.KeyRune, 'i', 0)); handled != nil {
		t.Fatal("expected i to be consumed when opening browser")
	}
	if !view.browserOpen {
		t.Fatal("expected browser to be open")
	}
	if view.browserTree.GetRoot() == nil {
		t.Fatal("expected browser tree root to be initialized")
	}
	preview := view.preview.GetText(false)
	if !strings.Contains(preview, "Directory:") {
		t.Fatalf("preview = %q, want directory preview", preview)
	}

	if handled := view.HandleInput(tcell.NewEventKey(tcell.KeyRune, 'i', 0)); handled != nil {
		t.Fatal("expected i to be consumed when closing browser")
	}
	if view.browserOpen {
		t.Fatal("expected browser to be closed")
	}
}

func TestDescribeBrowserEntryPreviewsFileContent(t *testing.T) {
	rootDir := t.TempDir()
	filePath := filepath.Join(rootDir, "config.txt")
	if err := os.WriteFile(filePath, []byte("line-one\nline-two\n"), 0o644); err != nil {
		t.Fatalf("write preview file: %v", err)
	}

	text, title := describeBrowserEntry(&layerBrowserEntry{path: filePath, isDir: false})
	if title != " File Preview " {
		t.Fatalf("title = %q", title)
	}
	if !strings.Contains(text, "line-one") || !strings.Contains(text, "line-two") {
		t.Fatalf("text = %q, want file content preview", text)
	}
}
