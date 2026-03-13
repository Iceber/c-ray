package ui

import (
	"context"
	"testing"
	"time"

	"github.com/icebergu/c-ray/pkg/models"
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

func TestNavigateToDetailSelectionPathDoesNotBlock(t *testing.T) {
	assertReturnsSoon(t, func() {
		app := NewApp(stubRuntime{})
		if afterDraw := app.tviewApp.GetAfterDrawFunc(); afterDraw != nil {
			afterDraw(nil)
		}

		app.nav.NavigateTo(PageContainerDetail)
		go app.tviewApp.QueueUpdateDraw(func() {
			app.detailView.SetContainer("container-1")
		})
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
		t.Fatal("operation did not complete before timeout; possible UI deadlock")
	}
}
