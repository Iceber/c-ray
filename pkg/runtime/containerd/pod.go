package containerd

import (
	"context"

	"github.com/icebergu/c-ray/pkg/runtime"
)

// podHandle implements runtime.Pod.
type podHandle struct {
	uid        string
	info       *runtime.PodInfo
	containers []runtime.Container
}

func (h *podHandle) UID() string { return h.uid }

func (h *podHandle) Info(_ context.Context) (*runtime.PodInfo, error) {
	return h.info, nil
}

func (h *podHandle) Containers(_ context.Context) ([]runtime.Container, error) {
	return h.containers, nil
}

// Compile-time interface check.
var _ runtime.Pod = (*podHandle)(nil)
