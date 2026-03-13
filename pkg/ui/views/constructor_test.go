package views

import (
	"context"
	"testing"
	"time"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/rivo/tview"
)

type stubRuntime struct{}

func (stubRuntime) Connect(ctx context.Context) error {
	return nil
}

func (stubRuntime) Close() error {
	return nil
}

func (stubRuntime) ListContainers(ctx context.Context) ([]*models.Container, error) {
	return nil, nil
}

func (stubRuntime) GetContainer(ctx context.Context, id string) (*models.Container, error) {
	return nil, nil
}

func (stubRuntime) GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{}, nil
}

func (stubRuntime) GetContainerRuntimeInfo(ctx context.Context, id string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{}, nil
}

func (stubRuntime) ListImages(ctx context.Context) ([]*models.Image, error) {
	return nil, nil
}

func (stubRuntime) GetImage(ctx context.Context, ref string) (*models.Image, error) {
	return nil, nil
}

func (stubRuntime) GetImageLayers(ctx context.Context, imageID string, snapshotter string, rwSnapshotKey string) ([]*models.ImageLayer, error) {
	return nil, nil
}

func (stubRuntime) GetImageConfigInfo(ctx context.Context, imageID string) (*models.ImageConfigInfo, error) {
	return nil, nil
}

func (stubRuntime) ListPods(ctx context.Context) ([]*models.Pod, error) {
	return nil, nil
}

func (stubRuntime) GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error) {
	return nil, nil
}

func (stubRuntime) GetContainerTop(ctx context.Context, id string) (*models.ProcessTop, error) {
	return &models.ProcessTop{}, nil
}

func (stubRuntime) GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error) {
	return nil, nil
}

func TestNewContainerDetailViewBeforeRunDoesNotDeadlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		_ = NewContainerDetailView(app, stubRuntime{}, context.Background())
	})
}

func TestProcessesViewRefreshBeforeRunDoesNotDeadlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		view := NewProcessesView(app, stubRuntime{}, context.Background())
		view.SetContainer("container-1")
		if err := view.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
	})
}

func TestStorageViewRefreshBeforeRunDoesNotDeadlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		view := NewStorageView(app, stubRuntime{}, context.Background())
		view.SetContainer("container-1")
		view.SetDetail(&models.ContainerDetail{
			Container: models.Container{Image: "sha256:test"},
			ImageName: "demo:latest",
		})
		if err := view.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
	})
}

func TestSetContainerAfterAppStartDoesNotDeadlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		TrackApplicationLifecycle(app)
		appUpdateStateFor(app).started.Store(true)

		view := NewContainerDetailView(app, stubRuntime{}, context.Background())
		view.SetContainer("container-1")
	})
}

func TestEnterContainerDetailDoesNotDeadlock(t *testing.T) {
	// This test simulates the user action: select a container and enter detail view
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		TrackApplicationLifecycle(app)
		appUpdateStateFor(app).started.Store(true)

		detailView := NewContainerDetailView(app, stubRuntime{}, context.Background())

		// Simulate user selecting a container - this is what happens when pressing Enter
		// on a container in the list
		detailView.SetContainer("container-1")

		// Give goroutines time to start
		time.Sleep(100 * time.Millisecond)

		// Simulate switching tabs across the new detail pages.
		detailView.switchTab(DetailTabProcesses)
		time.Sleep(50 * time.Millisecond)
		detailView.switchTab(DetailTabFilesystem)
		time.Sleep(50 * time.Millisecond)
		detailView.switchTab(DetailTabRuntime)
		time.Sleep(50 * time.Millisecond)
		detailView.switchTab(DetailTabNetwork)
		time.Sleep(50 * time.Millisecond)
		detailView.switchTab(DetailTabSummary)
	})
}

func TestRapidTabSwitchingDoesNotDeadlock(t *testing.T) {
	// Test rapid tab switching which could trigger race conditions
	assertReturnsSoon(t, func() {
		app := tview.NewApplication()
		TrackApplicationLifecycle(app)
		appUpdateStateFor(app).started.Store(true)

		processesView := NewProcessesView(app, stubRuntime{}, context.Background())
		processesView.SetContainer("container-1")

		// Rapidly switch between modes
		for i := 0; i < 10; i++ {
			processesView.switchTab(ProcessTabTree)
			processesView.switchTab(ProcessTabTop)
		}
	})
}

func TestNetworkInfoViewRefreshBeforeRunDoesNotDeadlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		view := NewNetworkInfoView(stubRuntime{})
		view.SetContainer("container-1")
		if err := view.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
	})
}

func assertReturnsSoon(t *testing.T, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not complete before timeout; possible startup deadlock")
	}
}
