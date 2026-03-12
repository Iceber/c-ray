package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ContainerdRuntime implements the Runtime interface for containerd
type ContainerdRuntime struct {
	config           *runtime.Config
	client           *client.Client
	processCollector *sysinfo.ProcessCollector
	cgroupReader     *sysinfo.CGroupReader
	mountReader      *sysinfo.MountReader
}

// NewContainerdRuntime creates a new containerd runtime instance
func NewContainerdRuntime(config *runtime.Config) *ContainerdRuntime {
	processCollector, _ := sysinfo.NewProcessCollector()
	cgroupReader, _ := sysinfo.NewCGroupReader()

	return &ContainerdRuntime{
		config:           config,
		processCollector: processCollector,
		cgroupReader:     cgroupReader,
		mountReader:      sysinfo.NewMountReader(),
	}
}

// Connect establishes connection to containerd
func (r *ContainerdRuntime) Connect(ctx context.Context) error {
	if r.client != nil {
		return nil // Already connected
	}

	// Create containerd client
	c, err := client.New(
		r.config.SocketPath,
		client.WithDefaultNamespace(r.config.Namespace),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to containerd at %s: %w", r.config.SocketPath, err)
	}

	r.client = c
	return nil
}

// Close closes the connection to containerd
func (r *ContainerdRuntime) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// ListContainers returns all containers
func (r *ContainerdRuntime) ListContainers(ctx context.Context) ([]*models.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Get all containers
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]*models.Container, 0, len(containers))
	for _, c := range containers {
		container, err := r.convertContainer(ctx, c)
		if err != nil {
			// Log error but continue with other containers
			continue
		}
		result = append(result, container)
	}

	return result, nil
}

// convertContainer converts containerd container to our model
func (r *ContainerdRuntime) convertContainer(ctx context.Context, c client.Container) (*models.Container, error) {
	info, err := c.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}

	container := &models.Container{
		ID:        info.ID,
		Image:     info.Image,
		CreatedAt: info.CreatedAt,
		Labels:    info.Labels,
	}

	// Extract name from labels (k8s uses io.kubernetes.container.name)
	if name, ok := info.Labels["io.kubernetes.container.name"]; ok {
		container.Name = name
	} else if name, ok := info.Labels["name"]; ok {
		container.Name = name
	} else {
		container.Name = info.ID[:12] // Use short ID as fallback
	}

	// Extract Pod information from labels
	if podName, ok := info.Labels["io.kubernetes.pod.name"]; ok {
		container.PodName = podName
	}
	if podNamespace, ok := info.Labels["io.kubernetes.pod.namespace"]; ok {
		container.PodNamespace = podNamespace
	}
	if podUID, ok := info.Labels["io.kubernetes.pod.uid"]; ok {
		container.PodUID = podUID
	}

	// Get task to determine status and PID
	task, err := c.Task(ctx, nil)
	if err != nil {
		// Container might not have a task (not running)
		container.Status = models.ContainerStatusStopped
		return container, nil
	}

	// Get task status
	status, err := task.Status(ctx)
	if err == nil {
		container.Status = convertStatus(string(status.Status))
		if string(status.Status) == "running" {
			container.StartedAt = time.Now() // TODO: Get actual start time
		}
	}

	// Get PID
	container.PID = task.Pid()

	return container, nil
}

// convertStatus converts containerd status to our model
func convertStatus(status string) models.ContainerStatus {
	switch status {
	case "created":
		return models.ContainerStatusCreated
	case "running":
		return models.ContainerStatusRunning
	case "paused":
		return models.ContainerStatusPaused
	case "stopped":
		return models.ContainerStatusStopped
	default:
		return models.ContainerStatusUnknown
	}
}

// GetContainer returns a specific container by ID
func (r *ContainerdRuntime) GetContainer(ctx context.Context, id string) (*models.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	c, err := r.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}

	return r.convertContainer(ctx, c)
}

// GetContainerDetail returns detailed information about a container
func (r *ContainerdRuntime) GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Load container
	c, err := r.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}

	// Get basic container info
	container, err := r.convertContainer(ctx, c)
	if err != nil {
		return nil, err
	}

	detail := &models.ContainerDetail{
		Container: *container,
	}

	// Get OCI spec
	spec, err := c.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	// Extract CGroup path
	if spec.Linux != nil && spec.Linux.CgroupsPath != "" {
		detail.CGroupPath = spec.Linux.CgroupsPath
	}

	// Extract mounts
	if spec.Mounts != nil {
		detail.Mounts = make([]*models.Mount, 0, len(spec.Mounts))
		for _, m := range spec.Mounts {
			detail.Mounts = append(detail.Mounts, &models.Mount{
				Source:      m.Source,
				Destination: m.Destination,
				Type:        m.Type,
				Options:     m.Options,
			})
		}
		detail.MountCount = len(detail.Mounts)
	}

	// Get task info
	task, err := c.Task(ctx, nil)
	if err == nil {
		// Get task PID
		detail.PID = task.Pid()

		// Get shim PID (parent of task)
		// Note: This is an approximation, actual shim PID might be different
		detail.ShimPID = detail.PID
	}

	// Get image info
	img, err := r.client.GetImage(ctx, container.Image)
	if err == nil {
		detail.ImageName = img.Name()
	}

	// Get snapshot info for image layers
	info, err := c.Info(ctx)
	if err == nil && info.SnapshotKey != "" {
		// Set the snapshot key (RW layer)
		detail.SnapshotKey = info.SnapshotKey
		// Set the snapshotter name
		detail.Snapshotter = info.Snapshotter

		// Get snapshotter
		snapshotter := r.client.SnapshotService(info.Snapshotter)
		if snapshotter != nil {
			// Get RW layer path (upperdir) using Mounts API
			if path, err := r.getRWLayerPathFromMounts(ctx, snapshotter, info.SnapshotKey); err == nil {
				detail.WritableLayerPath = path
			}

			// Get RW layer usage information
			if usage, err := snapshotter.Usage(ctx, info.SnapshotKey); err == nil {
				detail.RWLayerUsage = usage.Size
				detail.RWLayerInodes = usage.Inodes
			}
		}
	}

	// Get CGroup information
	if detail.CGroupPath != "" && r.cgroupReader != nil {
		limits, err := r.cgroupReader.ReadCGroupLimits(detail.CGroupPath)
		if err == nil {
			detail.CGroupLimits = limits
			// Detect CGroup version
			detail.CGroupVersion = int(r.cgroupReader.GetVersion())
		}
	}

	// Get mount information
	if detail.PID > 0 && r.mountReader != nil {
		mounts, err := r.mountReader.ReadMounts(int(detail.PID))
		if err == nil {
			detail.Mounts = mounts
			detail.MountCount = len(mounts)
		}
	}

	// Count processes
	if detail.PID > 0 && r.processCollector != nil {
		processes, err := r.processCollector.CollectContainerProcesses(detail.PID)
		if err == nil {
			detail.ProcessCount = len(processes)
		}
	}

	return detail, nil
}

// ListImages returns all images
func (r *ContainerdRuntime) ListImages(ctx context.Context) ([]*models.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	images, err := r.client.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	result := make([]*models.Image, 0, len(images))
	for _, img := range images {
		image := &models.Image{
			Name:      img.Name(),
			CreatedAt: img.Metadata().CreatedAt,
			Labels:    img.Metadata().Labels,
		}

		// Get image size
		size, err := img.Size(ctx)
		if err == nil {
			image.Size = size
		}

		// Get image digest
		if img.Target().Digest != "" {
			image.Digest = img.Target().Digest.String()
		}

		result = append(result, image)
	}

	return result, nil
}

// GetImage returns a specific image by name or digest
func (r *ContainerdRuntime) GetImage(ctx context.Context, ref string) (*models.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	img, err := r.client.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", ref, err)
	}

	image := &models.Image{
		Name:      img.Name(),
		CreatedAt: img.Metadata().CreatedAt,
		Labels:    img.Metadata().Labels,
	}

	// Get image size
	size, err := img.Size(ctx)
	if err == nil {
		image.Size = size
	}

	// Get image digest
	if img.Target().Digest != "" {
		image.Digest = img.Target().Digest.String()
	}

	return image, nil
}

// imageInfo holds all parsed image metadata for a given image reference
type imageInfo struct {
	target     ocispec.Descriptor // Original image target descriptor
	configDesc ocispec.Descriptor
	manifest   ocispec.Manifest
	diffIDs    []digest.Digest
}

// getImageInfo retrieves and parses all image metadata in one operation
// This ensures consistency across config, manifest, and diffIDs by using the same platform
func (r *ContainerdRuntime) getImageInfo(ctx context.Context, imageID string) (*imageInfo, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	img, err := r.client.GetImage(ctx, imageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", imageID, err)
	}

	cs := r.client.ContentStore()

	// Get config descriptor (platform-matched by containerd)
	configDesc, err := img.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image config: %w", err)
	}

	// Get manifest that matches the config (ensures platform consistency)
	manifest, err := r.getManifestForConfig(ctx, cs, img.Target(), configDesc.Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to get image manifest: %w", err)
	}

	// Parse diffIDs from config
	diffIDs, err := r.parseDiffIDsFromConfig(ctx, cs, configDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse diff ids: %w", err)
	}

	return &imageInfo{
		target:     img.Target(),
		configDesc: configDesc,
		manifest:   manifest,
		diffIDs:    diffIDs,
	}, nil
}

// GetImageConfigInfo returns metadata about the image config
func (r *ContainerdRuntime) GetImageConfigInfo(ctx context.Context, imageID string) (*models.ImageConfigInfo, error) {
	info, err := r.getImageInfo(ctx, imageID)
	if err != nil {
		return nil, err
	}

	return &models.ImageConfigInfo{
		Digest:      info.configDesc.Digest.String(),
		ContentPath: r.getContentPath(info.configDesc.Digest),
		Size:        info.configDesc.Size,
	}, nil
}

// GetImageLayers returns detailed layer information for an image (from top to base)
// If rwSnapshotKey is provided, layer paths are resolved from the RW layer's mount info
func (r *ContainerdRuntime) GetImageLayers(ctx context.Context, imageID string, snapshotterName string, rwSnapshotKey string) ([]*models.ImageLayer, error) {
	info, err := r.getImageInfo(ctx, imageID)
	if err != nil {
		return nil, err
	}

	if len(info.manifest.Layers) != len(info.diffIDs) {
		return nil, fmt.Errorf("layer count mismatch: manifest has %d layers, config has %d diff ids",
			len(info.manifest.Layers), len(info.diffIDs))
	}

	return r.buildImageLayers(ctx, info.manifest, info.diffIDs, snapshotterName, rwSnapshotKey)
}

// buildImageLayers constructs ImageLayer models from manifest and diffIDs
// Uses RW Layer mounts to get all Read-Only Layer paths in one call
func (r *ContainerdRuntime) buildImageLayers(ctx context.Context, manifest ocispec.Manifest, diffIDs []digest.Digest, snapshotterName string, rwSnapshotKey string) ([]*models.ImageLayer, error) {
	// Use provided snapshotter name or default to overlayfs
	if snapshotterName == "" {
		snapshotterName = "overlayfs"
	}
	snapshotter := r.client.SnapshotService(snapshotterName)
	layerCount := len(manifest.Layers)
	layers := make([]*models.ImageLayer, layerCount)

	// Calculate Chain IDs from base to top
	chainIDs := r.calculateChainIDs(diffIDs)

	// Get Read-Only Layer paths from RW Layer mounts (single call)
	var roPaths []string
	if rwSnapshotKey != "" {
		roPaths = r.getReadOnlyLayerPathsFromMounts(ctx, snapshotter, rwSnapshotKey)
	}

	// Build layers from base to top (natural order matching diff_ids and chainIDs)
	// layers[0] = base layer, layers[n-1] = top layer
	for i := 0; i < layerCount; i++ {
		chainID := chainIDs[i].String()
		layer := &models.ImageLayer{
			Index:              i,
			CompressedDigest:   manifest.Layers[i].Digest.String(),
			UncompressedDigest: diffIDs[i].String(),
			Size:               manifest.Layers[i].Size,
			ContentPath:        r.getContentPath(manifest.Layers[i].Digest),
			SnapshotKey:        chainID,
		}

		// Get compression type from media type
		layer.CompressionType = r.getCompressionType(manifest.Layers[i].MediaType)

		// Check if snapshot exists
		if _, err := snapshotter.Stat(ctx, chainID); err == nil {
			layer.SnapshotExists = true
			// Map Read-Only Layer path from pre-fetched paths
			// roPaths order: [top, mid..., base]
			// layers order: [base(0), mid..., top(n-1)]
			// So roPaths index = (layerCount - 1 - i)
			if len(roPaths) > 0 && i < len(roPaths) {
				roIndex := len(roPaths) - 1 - i
				if roIndex >= 0 && roIndex < len(roPaths) {
					layer.SnapshotPath = roPaths[roIndex]
				}
			}

			// Get usage information for this layer
			if usage, err := snapshotter.Usage(ctx, chainID); err == nil {
				layer.UsageSize = usage.Size
				layer.UsageInodes = usage.Inodes
			}
		}

		layers[i] = layer
	}

	return layers, nil
}

// getReadOnlyLayerPathsFromMounts extracts Read-Only Layer paths from RW Layer mounts
// Returns paths in order: [top, mid..., base] (matching lowerdir order)
func (r *ContainerdRuntime) getReadOnlyLayerPathsFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, rwKey string) []string {

	mounts, err := snapshotter.Mounts(ctx, rwKey)
	if err != nil {
		return nil
	}

	for _, m := range mounts {
		if m.Type == "overlay" {
			for _, opt := range m.Options {
				if len(opt) > 9 && opt[:9] == "lowerdir=" {
					// lowerdir format: /path/to/top:/path/to/mid:/path/to/base
					return strings.Split(opt[9:], ":")
				}
			}
		}
	}

	return nil
}

// getRWLayerPathFromMounts retrieves RW layer path (upperdir) using Snapshotter Mounts API
func (r *ContainerdRuntime) getRWLayerPathFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, key string) (string, error) {
	mounts, err := snapshotter.Mounts(ctx, key)
	if err != nil {
		return "", err
	}

	// Parse mount options to find upperdir (RW layer)
	for _, m := range mounts {
		if m.Type == "overlay" {
			for _, opt := range m.Options {
				if len(opt) > 9 && opt[:9] == "upperdir=" {
					return opt[9:], nil
				}
			}
		}
	}

	return "", fmt.Errorf("no upperdir found in mounts")
}

// getManifestForConfig finds the manifest that contains the given config digest
// This handles multi-arch images by iterating through the index to find the matching platform
func (r *ContainerdRuntime) getManifestForConfig(ctx context.Context, cs content.Store, target ocispec.Descriptor, configDigest digest.Digest) (ocispec.Manifest, error) {
	// If target is already a manifest, check if it matches
	if images.IsManifestType(target.MediaType) {
		manifest, err := images.Manifest(ctx, cs, target, nil)
		if err != nil {
			return ocispec.Manifest{}, err
		}
		if manifest.Config.Digest != configDigest {
			return ocispec.Manifest{}, fmt.Errorf("manifest config digest mismatch")
		}
		return manifest, nil
	}

	// Target is likely an index (multi-arch), read and iterate through manifests
	data, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return ocispec.Manifest{}, fmt.Errorf("failed to read image index: %w", err)
	}

	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("failed to unmarshal image index: %w", err)
	}

	for _, desc := range index.Manifests {
		if !images.IsManifestType(desc.MediaType) {
			continue
		}
		manifest, err := images.Manifest(ctx, cs, desc, nil)
		if err != nil {
			continue
		}
		if manifest.Config.Digest == configDigest {
			return manifest, nil
		}
	}

	return ocispec.Manifest{}, fmt.Errorf("no manifest found with config digest %s", configDigest)
}

// parseDiffIDsFromConfig extracts RootFS DiffIDs directly from config descriptor
func (r *ContainerdRuntime) parseDiffIDsFromConfig(ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) ([]digest.Digest, error) {
	// Read config blob directly
	data, err := content.ReadBlob(ctx, cs, configDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config struct {
		RootFS struct {
			Type    string
			DiffIDs []digest.Digest `json:"diff_ids"`
		} `json:"rootfs"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return config.RootFS.DiffIDs, nil
}

// calculateChainIDs calculates Chain IDs from base to top
// Layer 0 (base): chain[0] = diffIDs[0]
// Layer n: chain[n] = sha256(chain[n-1] + " " + diffIDs[n])
// Note: Use String() to get full digest with "sha256:" prefix
func (r *ContainerdRuntime) calculateChainIDs(diffIDs []digest.Digest) []digest.Digest {
	chainIDs := make([]digest.Digest, len(diffIDs))

	for i, diffID := range diffIDs {
		if i == 0 {
			chainIDs[i] = diffID
		} else {
			chainIDs[i] = digest.FromString(chainIDs[i-1].String() + " " + diffID.String())
		}
	}

	return chainIDs
}

// getContentPath generates the content store blob path
func (r *ContainerdRuntime) getContentPath(dgst digest.Digest) string {
	algo := dgst.Algorithm()
	encoded := dgst.Encoded()

	// Default containerd content store path
	return fmt.Sprintf("/var/lib/containerd/io.containerd.content.v1.content/blobs/%s/%s", algo, encoded)
}

// getCompressionType extracts compression type from layer media type
func (r *ContainerdRuntime) getCompressionType(mediaType string) string {
	switch mediaType {
	// Gzip compressed
	case images.MediaTypeDockerSchema2LayerGzip,
		images.MediaTypeDockerSchema2LayerForeignGzip,
		ocispec.MediaTypeImageLayerGzip:
		return "gzip"
	// Zstd compressed
	case images.MediaTypeDockerSchema2LayerZstd,
		ocispec.MediaTypeImageLayerZstd:
		return "zstd"
	// Uncompressed
	case images.MediaTypeDockerSchema2Layer,
		images.MediaTypeDockerSchema2LayerForeign,
		ocispec.MediaTypeImageLayer:
		return ""
	default:
		// Try to extract from OCI media type with + suffix
		if strings.Contains(mediaType, "+gzip") {
			return "gzip"
		}
		if strings.Contains(mediaType, "+zstd") {
			return "zstd"
		}
		return ""
	}
}

// ListPods returns all pods
func (r *ContainerdRuntime) ListPods(ctx context.Context) ([]*models.Pod, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Get all containers first
	containers, err := r.ListContainers(ctx)
	if err != nil {
		return nil, err
	}

	// Group containers by pod
	podMap := make(map[string]*models.Pod)
	for _, container := range containers {
		// Skip containers without pod information
		if container.PodUID == "" {
			continue
		}

		pod, exists := podMap[container.PodUID]
		if !exists {
			pod = &models.Pod{
				Name:       container.PodName,
				Namespace:  container.PodNamespace,
				UID:        container.PodUID,
				Containers: make([]*models.Container, 0),
			}
			podMap[container.PodUID] = pod
		}

		pod.Containers = append(pod.Containers, container)
	}

	// Convert map to slice
	result := make([]*models.Pod, 0, len(podMap))
	for _, pod := range podMap {
		result = append(result, pod)
	}

	return result, nil
}

// GetContainerProcesses returns process information for a container
func (r *ContainerdRuntime) GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if r.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	// Get container to find PID
	container, err := r.GetContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	if container.PID == 0 {
		return nil, fmt.Errorf("container is not running")
	}

	// Collect processes
	return r.processCollector.CollectContainerProcesses(container.PID)
}

// GetContainerTop returns top-like process information
func (r *ContainerdRuntime) GetContainerTop(ctx context.Context, id string) (*models.ProcessTop, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if r.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	// Get container to find PID
	container, err := r.GetContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	if container.PID == 0 {
		return nil, fmt.Errorf("container is not running")
	}

	// Collect top information
	return r.processCollector.CollectProcessTop(container.PID)
}

// GetContainerMounts returns mount information for a container
func (r *ContainerdRuntime) GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if r.mountReader == nil {
		return nil, fmt.Errorf("mount reader not initialized")
	}

	// Get container to find PID
	container, err := r.GetContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	if container.PID == 0 {
		return nil, fmt.Errorf("container is not running")
	}

	// Read mounts
	return r.mountReader.ReadMounts(int(container.PID))
}
