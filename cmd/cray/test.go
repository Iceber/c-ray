package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/icebergu/c-ray/pkg/runtime"
	runtimecontainerd "github.com/icebergu/c-ray/pkg/runtime/containerd"
	runtimecrio "github.com/icebergu/c-ray/pkg/runtime/crio"
	runtimedocker "github.com/icebergu/c-ray/pkg/runtime/docker"
)

const (
	defaultCRIContainerdNamespace   = "k8s.io"
	defaultPlainContainerdNamespace = "default"
)

func resolveContainerdNamespace(configuredNamespace, socketPath string, supportsCRI func(string) bool) string {
	if configuredNamespace != "" {
		return configuredNamespace
	}
	if supportsCRI != nil && supportsCRI(socketPath) {
		return defaultCRIContainerdNamespace
	}
	return defaultPlainContainerdNamespace
}

// newRuntime creates a runtime.Runtime using the detected runtime type.
func newRuntime(config *runtime.Config) (runtime.Runtime, error) {
	runtimeType, resolvedSocket, err := detectRuntime(config.SocketPath)
	if err != nil {
		return nil, err
	}
	config.SocketPath = resolvedSocket

	switch runtimeType {
	case "crio":
		fmt.Fprintf(os.Stderr, "[runtime] detected CRI-O (socket: %s)\n", resolvedSocket)
		return runtimecrio.New(config), nil
	case "docker":
		fmt.Fprintf(os.Stderr, "[runtime] detected Docker (socket: %s)\n", resolvedSocket)
		return runtimedocker.New(config), nil
	default:
		config.Namespace = resolveContainerdNamespace(config.Namespace, resolvedSocket, probeCRISocket)
		fmt.Fprintf(os.Stderr, "[runtime] detected containerd (socket: %s, namespace: %s)\n", resolvedSocket, config.Namespace)
		return runtimecontainerd.New(config), nil
	}
}

func runTests(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: cray test <command>")
		fmt.Println("\nAvailable commands:")
		fmt.Println("  Containers:")
		fmt.Println("    list-containers                    List all containers")
		fmt.Println("    container-info <id>                Show container info")
		fmt.Println("    container-config <id>              Show container config")
		fmt.Println("    container-state <id>               Show container state")
		fmt.Println("    container-runtime <id>             Show container runtime profile")
		fmt.Println("    container-mounts <id>              Show container mounts")
		fmt.Println("    container-network <id>             Show container network")
		fmt.Println("    container-storage <id>             Show container storage / layers")
		fmt.Println("    container-processes <id>           Show container processes")
		fmt.Println("    container-process-stats <id> <pid> Show single process stats")
		fmt.Println("    container-image <id>               Show container's image info")
		fmt.Println("    container-all <id>                 Show all container details")
		fmt.Println("\n  Images:")
		fmt.Println("    list-images                        List all images")
		fmt.Println("    image-info <ref>                   Show image info")
		fmt.Println("    image-config <ref>                 Show image config")
		fmt.Println("    image-layers <ref> [snapshotter]   Show image layers")
		fmt.Println("\n  Pods:")
		fmt.Println("    list-pods                          List all pods")
		fmt.Println("    pod-info <uid>                     Show pod details")
		fmt.Println("\n  Runtime:")
		fmt.Println("    runtime-info                       Show runtime / storage backend info")
		os.Exit(1)
	}

	command := args[0]

	config := &runtime.Config{
		SocketPath: socketPath,
		Namespace:  namespace,
		Timeout:    timeout,
	}

	rt, err := newRuntime(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to detect runtime: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := rt.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	switch command {
	case "list-containers":
		listContainers(ctx, rt)
	case "container-info":
		requireArg(args, "container-info <id>")
		containerInfo(ctx, rt, args[1])
	case "container-config":
		requireArg(args, "container-config <id>")
		containerConfig(ctx, rt, args[1])
	case "container-state":
		requireArg(args, "container-state <id>")
		containerState(ctx, rt, args[1])
	case "container-runtime":
		requireArg(args, "container-runtime <id>")
		containerRuntime(ctx, rt, args[1])
	case "container-mounts":
		requireArg(args, "container-mounts <id>")
		containerMounts(ctx, rt, args[1])
	case "container-network":
		requireArg(args, "container-network <id>")
		containerNetwork(ctx, rt, args[1])
	case "container-storage":
		requireArg(args, "container-storage <id>")
		containerStorage(ctx, rt, args[1])
	case "container-processes":
		requireArg(args, "container-processes <id>")
		containerProcesses(ctx, rt, args[1])
	case "container-process-stats":
		requireArgN(args, 3, "container-process-stats <id> <pid>")
		containerProcessStatsByPID(ctx, rt, args[1], args[2])
	case "container-image":
		requireArg(args, "container-image <id>")
		containerImage(ctx, rt, args[1])
	case "container-all":
		requireArg(args, "container-all <id>")
		containerAll(ctx, rt, args[1])
	case "list-images":
		listImages(ctx, rt)
	case "image-info":
		requireArg(args, "image-info <ref>")
		imageInfo(ctx, rt, args[1])
	case "image-config":
		requireArg(args, "image-config <ref>")
		imageConfig(ctx, rt, args[1])
	case "image-layers":
		requireArg(args, "image-layers <ref>")
		snap := ""
		if len(args) >= 3 {
			snap = args[2]
		}
		imageLayers(ctx, rt, args[1], snap)
	case "list-pods":
		listPods(ctx, rt)
	case "pod-info":
		requireArg(args, "pod-info <uid>")
		podInfo(ctx, rt, args[1])
	case "runtime-info":
		runtimeInfo(ctx, rt)
	default:
		fmt.Fprintf(os.Stderr, "Unknown test command: %s\n", command)
		os.Exit(1)
	}
}

func requireArg(args []string, usage string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: cray test %s\n", usage)
		os.Exit(1)
	}
}

func requireArgN(args []string, n int, usage string) {
	if len(args) < n {
		fmt.Fprintf(os.Stderr, "Usage: cray test %s\n", usage)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Container commands
// ---------------------------------------------------------------------------

func listContainers(ctx context.Context, rt runtime.Runtime) {
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	entries := make([]map[string]any, 0, len(containers))
	for _, c := range containers {
		entry := map[string]any{"handle_id": c.ID()}
		info, err := c.Info(ctx)
		if err != nil {
			entry["info_error"] = err.Error()
			entries = append(entries, entry)
			continue
		}
		entry["info"] = info
		entries = append(entries, entry)
	}

	printJSONSection("List Containers", map[string]any{
		"count":      len(entries),
		"containers": entries,
	})
}

func containerInfo(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	info, err := c.Info(ctx)
	exitOnErr("Info", err)

	printJSONSection(fmt.Sprintf("Container Info: %s", shortID(id)), map[string]any{
		"container_id": id,
		"info":         info,
	})
}

func containerConfig(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	cfg, err := c.Config(ctx)
	exitOnErr("Config", err)

	printJSONSection(fmt.Sprintf("Container Config: %s", shortID(id)), map[string]any{
		"container_id": id,
		"config":       cfg,
	})
}

func containerState(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	state, err := c.State(ctx)
	exitOnErr("State", err)

	printJSONSection(fmt.Sprintf("Container State: %s", shortID(id)), map[string]any{
		"container_id": id,
		"state":        state,
	})
}

func containerRuntime(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	profile, err := c.Runtime(ctx)
	exitOnErr("Runtime", err)

	printJSONSection(fmt.Sprintf("Container Runtime: %s", shortID(id)), map[string]any{
		"container_id": id,
		"runtime":      profile,
	})
}

func containerMounts(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	mounts, err := c.Mounts(ctx)
	exitOnErr("Mounts", err)

	printJSONSection(fmt.Sprintf("Container Mounts: %s", shortID(id)), map[string]any{
		"container_id": id,
		"count":        len(mounts),
		"mounts":       mounts,
	})
}

func containerNetwork(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	net, err := c.Network(ctx)
	exitOnErr("Network", err)

	printJSONSection(fmt.Sprintf("Container Network: %s", shortID(id)), map[string]any{
		"container_id": id,
		"network":      net,
	})
}

func containerStorage(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	storage, err := c.Storage(ctx)
	exitOnErr("Storage", err)

	rwStats, _ := c.RWLayerStats(ctx)

	// CRI-O extended storage introspection (auto-detected).
	var crioInfo *runtimecrio.ContainerInfo
	if inspector, ok := c.(runtimecrio.ContainerIntrospector); ok {
		crioInfo, err = inspector.CRIOContainerInfo(ctx)
		if err != nil {
			crioInfo = nil
		}
	}

	printJSONSection(fmt.Sprintf("Container Storage: %s", shortID(id)), map[string]any{
		"container_id":   id,
		"storage":        storage,
		"rw_layer_stats": rwStats,
		"crio_storage":   crioInfo,
	})
}

func containerProcesses(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	procs, err := c.Processes(ctx)
	exitOnErr("Processes", err)

	stats, err := c.ProcessStats(ctx)
	if err != nil {
		stats = nil
	}

	printJSONSection(fmt.Sprintf("Container Processes: %s", shortID(id)), map[string]any{
		"container_id": id,
		"count":        len(procs),
		"processes":    procs,
		"top_process":  stats,
	})
}

func containerProcessStatsByPID(ctx context.Context, rt runtime.Runtime, id, pid string) {
	c := mustGetContainer(ctx, rt, id)
	stats, err := c.GetProcessStats(ctx, pid)
	exitOnErr("GetProcessStats", err)

	printJSONSection(fmt.Sprintf("Process Stats: container=%s pid=%s", shortID(id), pid), map[string]any{
		"container_id": id,
		"pid":          pid,
		"stats":        stats,
	})
}

func containerImage(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	img, err := c.Image(ctx)
	exitOnErr("Image", err)

	info, err := img.Info(ctx)
	exitOnErr("Image.Info", err)
	cfg, err := img.Config(ctx)
	exitOnErr("Image.Config", err)

	printJSONSection(fmt.Sprintf("Container Image: %s", shortID(id)), map[string]any{
		"container_id": id,
		"ref":          img.Ref(),
		"info":         info,
		"config":       cfg,
	})
}

func containerAll(ctx context.Context, rt runtime.Runtime, id string) {
	containerInfo(ctx, rt, id)
	fmt.Println()
	containerConfig(ctx, rt, id)
	fmt.Println()
	containerState(ctx, rt, id)
	fmt.Println()
	containerRuntime(ctx, rt, id)
	fmt.Println()
	containerMounts(ctx, rt, id)
	fmt.Println()
	containerNetwork(ctx, rt, id)
	fmt.Println()
	containerStorage(ctx, rt, id)
	fmt.Println()
	containerProcesses(ctx, rt, id)
	fmt.Println()
	containerImage(ctx, rt, id)
}

// ---------------------------------------------------------------------------
// Image commands
// ---------------------------------------------------------------------------

func listImages(ctx context.Context, rt runtime.Runtime) {
	images, err := rt.ListImages(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	entries := make([]map[string]any, 0, len(images))
	for _, img := range images {
		entry := map[string]any{"ref": img.Ref()}
		info, err := img.Info(ctx)
		if err != nil {
			entry["info_error"] = err.Error()
			entries = append(entries, entry)
			continue
		}
		entry["info"] = info
		entries = append(entries, entry)
	}

	printJSONSection("List Images", map[string]any{
		"count":  len(entries),
		"images": entries,
	})
}

func imageInfo(ctx context.Context, rt runtime.Runtime, ref string) {
	img := mustGetImage(ctx, rt, ref)
	info, err := img.Info(ctx)
	exitOnErr("Info", err)

	// CRI-O extended image introspection (auto-detected).
	var crioInfo *runtimecrio.ImageInfo
	if inspector, ok := img.(runtimecrio.ImageIntrospector); ok {
		crioInfo, err = inspector.CRIOImageInfo(ctx)
		if err != nil {
			crioInfo = nil
		}
	}

	printJSONSection(fmt.Sprintf("Image Info: %s", ref), map[string]any{
		"ref":          ref,
		"info":         info,
		"crio_storage": crioInfo,
	})
}

func imageConfig(ctx context.Context, rt runtime.Runtime, ref string) {
	img := mustGetImage(ctx, rt, ref)
	cfg, err := img.Config(ctx)
	exitOnErr("Config", err)

	printJSONSection(fmt.Sprintf("Image Config: %s", ref), map[string]any{
		"ref":    ref,
		"config": cfg,
	})
}

func imageLayers(ctx context.Context, rt runtime.Runtime, ref, snapshotter string) {
	img := mustGetImage(ctx, rt, ref)
	layers, err := img.Layers(ctx, runtime.LayerQuery{Snapshotter: snapshotter})
	exitOnErr("Layers", err)

	printJSONSection(fmt.Sprintf("Image Layers: %s", ref), map[string]any{
		"ref":         ref,
		"snapshotter": snapshotter,
		"count":       len(layers),
		"layers":      layers,
	})
}

func runtimeInfo(ctx context.Context, rt runtime.Runtime) {
	if inspector, ok := rt.(runtimecrio.StoreIntrospector); ok {
		info, err := inspector.CRIOStoreInfo(ctx)
		exitOnErr("CRI-O store info", err)
		printJSONSection("Runtime Info (CRI-O)", map[string]any{
			"runtime": "crio",
			"store":   info,
		})
	} else {
		printJSONSection("Runtime Info", map[string]any{
			"message": "No extended runtime introspection available for current backend.",
		})
	}
}

func printCRIOContainerStorage(id string, info *runtimecrio.ContainerInfo) {
	fmt.Printf("\n--- CRI-O Container Storage ---\n")
	fmt.Printf("Storage ID:       %s\n", info.ID)
	fmt.Printf("Names:            %s\n", joinOrDash(info.Names))
	fmt.Printf("Image ID:         %s\n", emptyDash(info.ImageID))
	fmt.Printf("RW Layer ID:      %s\n", emptyDash(info.LayerID))
	fmt.Printf("Created:          %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Directory:        %s\n", emptyDash(info.Directory))
	fmt.Printf("Run Directory:    %s\n", emptyDash(info.RunDirectory))
	fmt.Printf("Bundle Dir:       %s\n", emptyDash(info.BundleDir))
	fmt.Printf("Writable Layer:   %s\n", emptyDash(info.WritableLayerDir))
	if info.Size > 0 {
		fmt.Printf("Container Size:   %s\n", formatBytes(info.Size))
	}
	if info.Metadata != "" {
		fmt.Printf("Metadata:         %s\n", info.Metadata)
	}
	if info.MountLabel != "" {
		fmt.Printf("Mount Label:      %s\n", info.MountLabel)
	}
	if len(info.MountOptions) > 0 {
		fmt.Printf("Mount Options:    %s\n", strings.Join(info.MountOptions, ", "))
	}
	if len(info.ParentOwnerUIDs) > 0 || len(info.ParentOwnerGIDs) > 0 {
		fmt.Printf("Parent Owners:    uid=%v gid=%v\n", info.ParentOwnerUIDs, info.ParentOwnerGIDs)
	}
	if len(info.UIDMap) > 0 {
		fmt.Printf("UID Maps:         %s\n", formatCRIOIDMapEntries(info.UIDMap))
	}
	if len(info.GIDMap) > 0 {
		fmt.Printf("GID Maps:         %s\n", formatCRIOIDMapEntries(info.GIDMap))
	}
	if len(info.Flags) > 0 {
		fmt.Printf("Flags:            %s\n", formatAnyMap(info.Flags))
	}
	if len(info.DriverMetadata) > 0 {
		fmt.Printf("Driver Metadata:  %s\n", formatStringMap(info.DriverMetadata))
	}
	if len(info.BigData) > 0 {
		fmt.Println("\nBig Data:")
		for _, item := range info.BigData {
			fmt.Printf("  %-24s size=%s digest=%s\n", item.Name, formatMaybeBytes(item.Size), emptyDash(item.Digest))
		}
	}
	if layer := info.RWLayer; layer != nil {
		fmt.Println("\nRW Layer:")
		printCRIOLayerInfo(layer)
	}
	if info.Store != nil {
		fmt.Println("\nStore Summary:")
		fmt.Printf("  %s @ %s\n", info.Store.GraphDriverName, info.Store.GraphRoot)
		fmt.Printf("  runroot=%s imagestore=%s transient=%v\n", info.Store.RunRoot, emptyDash(info.Store.ImageStore), info.Store.TransientStore)
	}
}

func printCRIOImageStorage(ref string, info *runtimecrio.ImageInfo) {
	fmt.Printf("\n--- CRI-O Image Storage ---\n")
	fmt.Printf("Storage ID:       %s\n", info.ID)
	fmt.Printf("Names:            %s\n", joinOrDash(info.Names))
	if len(info.NamesHistory) > 0 {
		fmt.Printf("Names History:    %s\n", strings.Join(info.NamesHistory, ", "))
	}
	fmt.Printf("Digest:           %s\n", emptyDash(info.Digest))
	if len(info.Digests) > 0 {
		fmt.Printf("Digests:          %s\n", strings.Join(info.Digests, ", "))
	}
	fmt.Printf("Top Layer:        %s\n", emptyDash(info.TopLayer))
	if len(info.MappedTopLayers) > 0 {
		fmt.Printf("Mapped Layers:    %s\n", strings.Join(info.MappedTopLayers, ", "))
	}
	fmt.Printf("Read Only:        %v\n", info.ReadOnly)
	fmt.Printf("Directory:        %s\n", emptyDash(info.Directory))
	fmt.Printf("Run Directory:    %s\n", emptyDash(info.RunDirectory))
	if info.TopLayerPath != "" {
		fmt.Printf("Top Layer Path:   %s\n", info.TopLayerPath)
	}
	if info.Size > 0 {
		fmt.Printf("Image Size:       %s\n", formatBytes(info.Size))
	}
	if info.Metadata != "" {
		fmt.Printf("Metadata:         %s\n", info.Metadata)
	}
	if len(info.Flags) > 0 {
		fmt.Printf("Flags:            %s\n", formatAnyMap(info.Flags))
	}
	if len(info.TopLayerDriverMeta) > 0 {
		fmt.Printf("Top Layer Meta:   %s\n", formatStringMap(info.TopLayerDriverMeta))
	}
	if len(info.Manifests) > 0 {
		fmt.Println("\nManifests:")
		for _, manifest := range info.Manifests {
			fmt.Printf("  %-24s kind=%s mediaType=%s schema=%d digest=%s size=%s\n",
				manifest.Name,
				emptyDash(manifest.Kind),
				emptyDash(manifest.MediaType),
				manifest.SchemaVersion,
				emptyDash(manifest.Digest),
				formatMaybeBytes(manifest.Size),
			)
			if manifest.Config != nil {
				fmt.Printf("    Config:       %s %s %s\n",
					emptyDash(manifest.Config.MediaType),
					emptyDash(manifest.Config.Digest),
					formatMaybeBytes(manifest.Config.Size),
				)
				if manifest.LinkedConfig != "" {
					fmt.Printf("    Resolved:     %s (%s match)\n", manifest.LinkedConfig, manifest.ConfigMatch)
				} else {
					fmt.Printf("    Resolved:     -\n")
				}
			}
			if len(manifest.Layers) > 0 {
				fmt.Printf("    Layers:       %d\n", len(manifest.Layers))
				for i, layer := range manifest.Layers {
					fmt.Printf("      [%d] %s %s %s\n", i, emptyDash(layer.MediaType), emptyDash(layer.Digest), formatMaybeBytes(layer.Size))
				}
			}
			if len(manifest.Manifests) > 0 {
				fmt.Printf("    Entries:      %d\n", len(manifest.Manifests))
				for i, entry := range manifest.Manifests {
					platform := formatPlatform(entry.OS, entry.Architecture, entry.Variant)
					fmt.Printf("      [%d] %s %s %s %s\n", i, emptyDash(entry.MediaType), emptyDash(entry.Digest), formatMaybeBytes(entry.Size), platform)
				}
			}
		}
	}
	if len(info.Configs) > 0 {
		fmt.Println("\nConfigs:")
		for _, config := range info.Configs {
			fmt.Printf("  %-24s digest=%s size=%s\n", config.Name, emptyDash(config.Digest), formatMaybeBytes(config.Size))
			fmt.Printf("    Platform:     %s\n", formatPlatform(config.OS, config.Architecture, config.Variant))
			if len(config.ReferencedBy) > 0 {
				fmt.Printf("    Referenced:   %d manifest(s)\n", len(config.ReferencedBy))
				for _, ref := range config.ReferencedBy {
					fmt.Printf("      %s %s\n", ref.Name, emptyDash(ref.Digest))
				}
			} else if len(info.Manifests) > 0 {
				fmt.Printf("    Referenced:   -\n")
			}
			if config.Created != "" {
				fmt.Printf("    Created:      %s\n", config.Created)
			}
			if config.Author != "" {
				fmt.Printf("    Author:       %s\n", config.Author)
			}
			if config.RootFSType != "" {
				fmt.Printf("    RootFS:       %s (%d diff IDs)\n", config.RootFSType, len(config.DiffIDs))
			}
			if config.User != "" {
				fmt.Printf("    User:         %s\n", config.User)
			}
			if config.WorkingDir != "" {
				fmt.Printf("    Working Dir:  %s\n", config.WorkingDir)
			}
			if len(config.Entrypoint) > 0 {
				fmt.Printf("    Entrypoint:   %s\n", strings.Join(config.Entrypoint, " "))
			}
			if len(config.Cmd) > 0 {
				fmt.Printf("    Cmd:          %s\n", strings.Join(config.Cmd, " "))
			}
			if len(config.Env) > 0 {
				fmt.Printf("    Env:          %d vars\n", len(config.Env))
				for _, env := range config.Env {
					fmt.Printf("      %s\n", env)
				}
			}
			if len(config.ExposedPorts) > 0 {
				fmt.Printf("    Exposed:      %s\n", strings.Join(config.ExposedPorts, ", "))
			}
			if len(config.Volumes) > 0 {
				fmt.Printf("    Volumes:      %s\n", strings.Join(config.Volumes, ", "))
			}
			if config.StopSignal != "" {
				fmt.Printf("    Stop Signal:  %s\n", config.StopSignal)
			}
			if len(config.Healthcheck) > 0 {
				fmt.Printf("    Healthcheck:  %s\n", strings.Join(config.Healthcheck, " "))
			}
			if len(config.Labels) > 0 {
				fmt.Printf("    Labels:       %s\n", formatStringMap(config.Labels))
			}
			if len(config.Annotations) > 0 {
				fmt.Printf("    Annotations:  %s\n", formatStringMap(config.Annotations))
			}
			if config.HistoryCount > 0 {
				fmt.Printf("    History:      %d entries\n", config.HistoryCount)
			}
		}
	}
	if len(info.Signatures) > 0 {
		fmt.Println("\nSignatures:")
		for _, signature := range info.Signatures {
			fmt.Printf("  %-24s format=%s entries=%d digest=%s size=%s\n",
				signature.Name,
				emptyDash(signature.Format),
				signature.Entries,
				emptyDash(signature.Digest),
				formatMaybeBytes(signature.Size),
			)
			if signature.MediaType != "" {
				fmt.Printf("    Media Type:   %s\n", signature.MediaType)
			}
			if len(signature.Annotations) > 0 {
				fmt.Printf("    Annotations:  %s\n", formatStringMap(signature.Annotations))
			}
			if signature.Preview != "" {
				fmt.Printf("    Preview:      %s\n", signature.Preview)
			}
		}
	}
	if len(info.OtherBigData) > 0 {
		fmt.Println("\nOther Big Data:")
		for _, item := range info.OtherBigData {
			fmt.Printf("  %-24s size=%s digest=%s\n", item.Name, formatMaybeBytes(item.Size), emptyDash(item.Digest))
		}
	}
	if info.Store != nil {
		fmt.Println("\nStore Summary:")
		fmt.Printf("  %s @ %s\n", info.Store.GraphDriverName, info.Store.GraphRoot)
		fmt.Printf("  runroot=%s imagestore=%s transient=%v\n", info.Store.RunRoot, emptyDash(info.Store.ImageStore), info.Store.TransientStore)
	}
}

// ---------------------------------------------------------------------------
// Pod commands
// ---------------------------------------------------------------------------

func listPods(ctx context.Context, rt runtime.Runtime) {
	pods, err := rt.ListPods(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	entries := make([]map[string]any, 0, len(pods))
	for _, pod := range pods {
		entry := map[string]any{"uid": pod.UID()}
		info, err := pod.Info(ctx)
		if err != nil {
			entry["info_error"] = err.Error()
			entries = append(entries, entry)
			continue
		}
		containers, _ := pod.Containers(ctx)
		containerEntries := make([]map[string]any, 0, len(containers))
		for _, c := range containers {
			containerEntry := map[string]any{"handle_id": c.ID()}
			cInfo, _ := c.Info(ctx)
			if cInfo != nil {
				containerEntry["info"] = cInfo
			}
			containerEntries = append(containerEntries, containerEntry)
		}
		entry["info"] = info
		entry["containers"] = containerEntries
		entries = append(entries, entry)
	}

	printJSONSection("List Pods", map[string]any{
		"count": len(entries),
		"pods":  entries,
	})
}

func podInfo(ctx context.Context, rt runtime.Runtime, uid string) {
	pods, err := rt.ListPods(ctx)
	exitOnErr("ListPods", err)

	var target runtime.Pod
	for _, pod := range pods {
		if pod.UID() == uid {
			target = pod
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "Pod not found: %s\n", uid)
		os.Exit(1)
	}

	info, err := target.Info(ctx)
	exitOnErr("Pod.Info", err)

	containers, err := target.Containers(ctx)
	exitOnErr("Pod.Containers", err)

	containerEntries := make([]map[string]any, 0, len(containers))
	for _, c := range containers {
		entry := map[string]any{"handle_id": c.ID()}
		cInfo, err := c.Info(ctx)
		if err != nil {
			entry["info_error"] = err.Error()
			containerEntries = append(containerEntries, entry)
			continue
		}
		entry["info"] = cInfo
		containerEntries = append(containerEntries, entry)
	}

	printJSONSection(fmt.Sprintf("Pod Info: %s", uid), map[string]any{
		"uid":        uid,
		"info":       info,
		"containers": containerEntries,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustGetContainer(ctx context.Context, rt runtime.Runtime, id string) runtime.Container {
	c, err := rt.GetContainer(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting container %s: %v\n", id, err)
		os.Exit(1)
	}
	return c
}

func mustGetImage(ctx context.Context, rt runtime.Runtime, ref string) runtime.Image {
	img, err := rt.GetImage(ctx, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image %s: %v\n", ref, err)
		os.Exit(1)
	}
	return img
}

func exitOnErr(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s error: %v\n", label, err)
		os.Exit(1)
	}
}

func printJSONSection(title string, value any) {
	fmt.Printf("=== %s ===\n", title)
	data, err := marshalPrettyJSON(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON encode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func marshalPrettyJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func printProcessStats(ps *runtime.ProcessStats, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Printf("%sPID: %-8d CPU: %.2f%%  Mem: %.2f%%  RSS: %s  Cmd: %s\n",
		indent, ps.PID, ps.CPUPercent, ps.MemoryPercent,
		formatBytes(int64(ps.MemoryRSS)), ps.Command)
	for _, child := range ps.Children {
		printProcessStats(child, depth+1)
	}
}

func printCRIOLayerInfo(layer *runtimecrio.LayerInfo) {
	fmt.Printf("  ID:             %s\n", layer.ID)
	if layer.Parent != "" {
		fmt.Printf("  Parent:         %s\n", layer.Parent)
	}
	if len(layer.Names) > 0 {
		fmt.Printf("  Names:          %s\n", strings.Join(layer.Names, ", "))
	}
	if layer.Path != "" {
		fmt.Printf("  Path:           %s\n", layer.Path)
	}
	if layer.Metadata != "" {
		fmt.Printf("  Metadata:       %s\n", layer.Metadata)
	}
	if layer.MountLabel != "" {
		fmt.Printf("  Mount Label:    %s\n", layer.MountLabel)
	}
	if layer.MountPoint != "" {
		fmt.Printf("  Mount Point:    %s (count=%d)\n", layer.MountPoint, layer.MountCount)
	}
	if layer.CompressedDigest != "" {
		fmt.Printf("  Compressed:     %s (%s)\n", layer.CompressedDigest, formatMaybeBytes(layer.CompressedSize))
	}
	if layer.UncompressedDigest != "" {
		fmt.Printf("  Uncompressed:   %s (%s)\n", layer.UncompressedDigest, formatMaybeBytes(layer.UncompressedSize))
	}
	if layer.TOCDigest != "" {
		fmt.Printf("  TOC:            %s\n", layer.TOCDigest)
	}
	if layer.CompressionType != "" {
		fmt.Printf("  Compression:    %s\n", layer.CompressionType)
	}
	if len(layer.BigDataNames) > 0 {
		fmt.Printf("  Big Data:       %s\n", strings.Join(layer.BigDataNames, ", "))
	}
	if len(layer.UIDMap) > 0 {
		fmt.Printf("  UID Maps:       %s\n", formatCRIOIDMapEntries(layer.UIDMap))
	}
	if len(layer.GIDMap) > 0 {
		fmt.Printf("  GID Maps:       %s\n", formatCRIOIDMapEntries(layer.GIDMap))
	}
	if len(layer.UsedUIDs) > 0 {
		fmt.Printf("  Used UIDs:      %v\n", layer.UsedUIDs)
	}
	if len(layer.UsedGIDs) > 0 {
		fmt.Printf("  Used GIDs:      %v\n", layer.UsedGIDs)
	}
	if len(layer.Flags) > 0 {
		fmt.Printf("  Flags:          %s\n", formatAnyMap(layer.Flags))
	}
	if len(layer.DriverMetadata) > 0 {
		fmt.Printf("  Driver Meta:    %s\n", formatStringMap(layer.DriverMetadata))
	}
	fmt.Printf("  Read Only:      %v\n", layer.ReadOnly)
}

func formatStringMap(values map[string]string) string {
	parts := make([]string, 0, len(values))
	for _, key := range sortedStringKeys(values) {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func formatAnyMap(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func formatIDMapEntries(values []runtime.IDMapEntry) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d:%d:%d", value.ContainerID, value.HostID, value.Size))
	}
	return strings.Join(parts, ", ")
}

func formatCRIOIDMapEntries(values []runtimecrio.IDMapEntry) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d:%d:%d", value.ContainerID, value.HostID, value.Size))
	}
	return strings.Join(parts, ", ")
}

func formatMaybeBytes(size int64) string {
	if size < 0 {
		return "-"
	}
	return formatBytes(size)
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func formatLayerBackend(backend *runtime.LayerBackend) string {
	if backend == nil {
		return "-"
	}
	name := emptyDash(backend.Name)
	switch backend.Kind {
	case runtime.LayerBackendDockerGraphDriver:
		return "Docker Graph Driver / " + name
	case runtime.LayerBackendContainerdSnapshotter:
		return "Containerd Snapshotter / " + name
	case runtime.LayerBackendContainersStorage:
		return "Containers Storage Driver / " + name
	default:
		return name
	}
}

func formatPlatform(osName, arch, variant string) string {
	parts := make([]string, 0, 3)
	if osName != "" {
		parts = append(parts, osName)
	}
	if arch != "" {
		parts = append(parts, arch)
	}
	if variant != "" {
		parts = append(parts, variant)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "/")
}
