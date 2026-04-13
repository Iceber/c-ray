package cri

import (
	"context"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
)

// MetadataClient wraps the low-level CRI client and provides runtime-typed
// access to container metadata, keeping raw CRI types internal to this package.
type MetadataClient struct {
	inner *Client
}

// NewMetadataClient creates a CRI metadata client for the given socket.
func NewMetadataClient(socketPath string) *MetadataClient {
	return &MetadataClient{inner: NewClient(socketPath)}
}

// ---------------------------------------------------------------------------
// Container mount set (opaque wrapper)
// ---------------------------------------------------------------------------

// ContainerMountSet holds CRI mount data for later merge processing.
// It is opaque to consumers outside this package.
type ContainerMountSet struct {
	raw *ContainerMounts
}

// InspectContainerMounts fetches CRI-declared mounts for a container.
func (c *MetadataClient) InspectContainerMounts(ctx context.Context, containerID string) (*ContainerMountSet, error) {
	raw, err := c.inner.InspectContainerMounts(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return &ContainerMountSet{raw: raw}, nil
}

// ---------------------------------------------------------------------------
// Container status
// ---------------------------------------------------------------------------

// ContainerStatus holds CRI-sourced container lifecycle fields.
type ContainerStatus struct {
	Status       string
	StartedAt    time.Time
	FinishedAt   time.Time
	ExitCode     *int32
	Reason       string
	RestartCount *uint32
	Envs         []ContainerEnv
	PID          uint32 // from CRI verbose info (CRI-O)

	// Fields from ContainerStatus (authoritative source; ListContainers may omit labels/annotations).
	Annotations map[string]string
	Labels      map[string]string
	Image       string
	ImageRef    string
	Name        string

	// Stdio-related CRI fields.
	TTY           *bool
	Stdin         *bool
	StdinOnce     *bool
	ConfigLogPath string
	StatusLogPath string
}

// InspectContainerStatus fetches CRI lifecycle metadata for a container.
func (c *MetadataClient) InspectContainerStatus(ctx context.Context, containerID string) (*ContainerStatus, error) {
	raw, err := c.inner.InspectContainerStatus(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return convertContainerStatus(raw), nil
}

func convertContainerStatus(raw *ContainerStatusInfo) *ContainerStatus {
	if raw == nil {
		return nil
	}
	s := &ContainerStatus{
		Status:        raw.Status,
		StartedAt:     raw.StartedAt,
		FinishedAt:    raw.FinishedAt,
		ExitCode:      raw.ExitCode,
		Reason:        raw.Reason,
		RestartCount:  raw.RestartCount,
		PID:           raw.PID,
		Annotations:   raw.Annotations,
		Labels:        raw.Labels,
		Image:         raw.Image,
		ImageRef:      raw.ImageRef,
		Name:          raw.Name,
		TTY:           raw.TTY,
		Stdin:         raw.Stdin,
		StdinOnce:     raw.StdinOnce,
		ConfigLogPath: raw.ConfigLogPath,
		StatusLogPath: raw.StatusLogPath,
	}
	if len(raw.Envs) > 0 {
		s.Envs = append([]ContainerEnv(nil), raw.Envs...)
	}
	return s
}

// ---------------------------------------------------------------------------
// Pod sandbox network
// ---------------------------------------------------------------------------

// ApplyPodSandboxNetwork fetches CRI pod sandbox network metadata and
// merges it into dst. Returns nil on success.
func (c *MetadataClient) ApplyPodSandboxNetwork(ctx context.Context, sandboxID string, dst *runtime.PodNetworkInfo) error {
	raw, err := c.inner.InspectPodSandboxNetwork(ctx, sandboxID)
	if err != nil {
		return err
	}
	if raw != nil {
		ApplyCRINetwork(dst, raw)
	}
	return nil
}
