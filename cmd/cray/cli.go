package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

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

// ---------------------------------------------------------------------------
// CLI dispatch: container(s)
// ---------------------------------------------------------------------------

func printContainerUsage() {
	fmt.Println("Usage: cray container(s) <action> [args]")
	fmt.Println()
	fmt.Println("Actions:")
	fmt.Println("  list, ls                     List all containers")
	fmt.Println("  info <id>                    Show container info")
	fmt.Println("  config <id>                  Show container config")
	fmt.Println("  state <id>                   Show container state")
	fmt.Println("  runtime <id>                 Show container runtime profile")
	fmt.Println("  mounts <id>                  Show container mounts")
	fmt.Println("  network <id>                 Show container network")
	fmt.Println("  storage <id>                 Show container storage / layers")
	fmt.Println("  stdio <id>                   Show container stdio (stdin/stdout/stderr)")
	fmt.Println("  processes <id>               Show container processes")
	fmt.Println("  process-stats <id> <pid>     Show single process stats")
	fmt.Println("  cgroup <id>                  Show container cgroup info and live stats")
	fmt.Println("  image <id>                   Show container's image info")
	fmt.Println("  all <id>                     Show all container details")
}

func runContainerCommand(args []string) {
	if len(args) < 1 {
		printContainerUsage()
		os.Exit(1)
	}

	action := args[0]
	rest := args[1:]

	if action == "help" || action == "-h" || action == "--help" {
		printContainerUsage()
		return
	}

	ctx, rt := mustConnect()
	defer rt.Close()

	switch action {
	case "list", "ls":
		listContainers(ctx, rt)
	case "info":
		requireActionArg(rest, "container info <id>")
		containerInfo(ctx, rt, rest[0])
	case "config":
		requireActionArg(rest, "container config <id>")
		containerConfig(ctx, rt, rest[0])
	case "state":
		requireActionArg(rest, "container state <id>")
		containerState(ctx, rt, rest[0])
	case "runtime":
		requireActionArg(rest, "container runtime <id>")
		containerRuntime(ctx, rt, rest[0])
	case "mounts":
		requireActionArg(rest, "container mounts <id>")
		containerMounts(ctx, rt, rest[0])
	case "network":
		requireActionArg(rest, "container network <id>")
		containerNetwork(ctx, rt, rest[0])
	case "storage":
		requireActionArg(rest, "container storage <id>")
		containerStorage(ctx, rt, rest[0])
	case "stdio":
		requireActionArg(rest, "container stdio <id>")
		containerStdio(ctx, rt, rest[0])
	case "processes":
		requireActionArg(rest, "container processes <id>")
		containerProcesses(ctx, rt, rest[0])
	case "process-stats":
		requireActionArgN(rest, 2, "container process-stats <id> <pid>")
		containerProcessStatsByPID(ctx, rt, rest[0], rest[1])
	case "cgroup":
		requireActionArg(rest, "container cgroup <id>")
		containerCGroup(ctx, rt, rest[0])
	case "image":
		requireActionArg(rest, "container image <id>")
		containerImage(ctx, rt, rest[0])
	case "all":
		requireActionArg(rest, "container all <id>")
		containerAll(ctx, rt, rest[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown container action: %s\n\n", action)
		printContainerUsage()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// CLI dispatch: image(s)
// ---------------------------------------------------------------------------

func printImageUsage() {
	fmt.Println("Usage: cray image(s) <action> [args]")
	fmt.Println()
	fmt.Println("Actions:")
	fmt.Println("  list, ls                     List all images")
	fmt.Println("  info <ref>                   Show image info")
	fmt.Println("  config <ref>                 Show image config")
	fmt.Println("  layers <ref> [snapshotter]   Show image layers")
}

func runImageCommand(args []string) {
	if len(args) < 1 {
		printImageUsage()
		os.Exit(1)
	}

	action := args[0]
	rest := args[1:]

	if action == "help" || action == "-h" || action == "--help" {
		printImageUsage()
		return
	}

	ctx, rt := mustConnect()
	defer rt.Close()

	switch action {
	case "list", "ls":
		listImages(ctx, rt)
	case "info":
		requireActionArg(rest, "image info <ref>")
		imageInfo(ctx, rt, rest[0])
	case "config":
		requireActionArg(rest, "image config <ref>")
		imageConfig(ctx, rt, rest[0])
	case "layers":
		requireActionArg(rest, "image layers <ref>")
		snap := ""
		if len(rest) >= 2 {
			snap = rest[1]
		}
		imageLayers(ctx, rt, rest[0], snap)
	default:
		fmt.Fprintf(os.Stderr, "Unknown image action: %s\n\n", action)
		printImageUsage()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// CLI dispatch: pod(s)
// ---------------------------------------------------------------------------

func printPodUsage() {
	fmt.Println("Usage: cray pod(s) <action> [args]")
	fmt.Println()
	fmt.Println("Actions:")
	fmt.Println("  list, ls                     List all pods")
	fmt.Println("  info <uid>                   Show pod details")
}

func runPodCommand(args []string) {
	if len(args) < 1 {
		printPodUsage()
		os.Exit(1)
	}

	action := args[0]
	rest := args[1:]

	if action == "help" || action == "-h" || action == "--help" {
		printPodUsage()
		return
	}

	ctx, rt := mustConnect()
	defer rt.Close()

	switch action {
	case "list", "ls":
		listPods(ctx, rt)
	case "info":
		requireActionArg(rest, "pod info <uid>")
		podInfo(ctx, rt, rest[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown pod action: %s\n\n", action)
		printPodUsage()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// CLI dispatch: runtime
// ---------------------------------------------------------------------------

func printRuntimeUsage() {
	fmt.Println("Usage: cray runtime <action>")
	fmt.Println()
	fmt.Println("Actions:")
	fmt.Println("  info                         Show runtime / storage backend info")
}

func runRuntimeCommand(args []string) {
	if len(args) < 1 {
		printRuntimeUsage()
		os.Exit(1)
	}

	action := args[0]

	if action == "help" || action == "-h" || action == "--help" {
		printRuntimeUsage()
		return
	}

	ctx, rt := mustConnect()
	defer rt.Close()

	switch action {
	case "info":
		runtimeInfo(ctx, rt)
	default:
		fmt.Fprintf(os.Stderr, "Unknown runtime action: %s\n\n", action)
		printRuntimeUsage()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Connect helper
// ---------------------------------------------------------------------------

func mustConnect() (context.Context, runtime.Runtime) {
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

	return ctx, rt
}

func requireActionArg(rest []string, usage string) {
	if len(rest) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: cray %s\n", usage)
		os.Exit(1)
	}
}

func requireActionArgN(rest []string, n int, usage string) {
	if len(rest) < n {
		fmt.Fprintf(os.Stderr, "Usage: cray %s\n", usage)
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

	entries := make([]any, 0, len(containers))
	for _, c := range containers {
		info, err := c.Info(ctx)
		if err != nil {
			entries = append(entries, map[string]any{
				"id":    c.ID(),
				"error": err.Error(),
			})
			continue
		}
		entries = append(entries, info)
	}

	printJSON(entries)
}

func containerInfo(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	info, err := c.Info(ctx)
	exitOnErr("Info", err)

	printJSON(info)
}

func containerConfig(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	cfg, err := c.Config(ctx)
	exitOnErr("Config", err)

	printJSON(cfg)
}

func containerState(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	state, err := c.State(ctx)
	exitOnErr("State", err)

	printJSON(state)
}

func containerRuntime(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	profile, err := c.Runtime(ctx)
	exitOnErr("Runtime", err)

	printJSON(profile)
}

func containerMounts(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	mounts, err := c.Mounts(ctx)
	exitOnErr("Mounts", err)

	printJSON(mounts)
}

func containerNetwork(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	net, err := c.Network(ctx)
	exitOnErr("Network", err)

	printJSON(net)
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

	result := map[string]any{
		"storage":        storage,
		"rw_layer_stats": rwStats,
	}
	if crioInfo != nil {
		result["crio_storage"] = crioInfo
	}
	printJSON(result)
}

func containerProcesses(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	procs, err := c.Processes(ctx)
	exitOnErr("Processes", err)

	stats, err := c.ProcessStats(ctx)
	if err != nil {
		stats = nil
	}

	result := map[string]any{
		"processes":   procs,
		"top_process": stats,
	}
	printJSON(result)
}

func containerProcessStatsByPID(ctx context.Context, rt runtime.Runtime, id, pid string) {
	c := mustGetContainer(ctx, rt, id)
	stats, err := c.GetProcessStats(ctx, pid)
	exitOnErr("GetProcessStats", err)

	printJSON(stats)
}

func containerImage(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	img, err := c.Image(ctx)
	exitOnErr("Image", err)

	info, err := img.Info(ctx)
	exitOnErr("Image.Info", err)
	cfg, err := img.Config(ctx)
	exitOnErr("Image.Config", err)

	printJSON(map[string]any{
		"ref":    img.Ref(),
		"info":   info,
		"config": cfg,
	})
}

func containerCGroup(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	info, err := c.CGroup(ctx)
	exitOnErr("CGroup", err)

	printJSON(info)
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
	containerStdio(ctx, rt, id)
	fmt.Println()
	containerProcesses(ctx, rt, id)
	fmt.Println()
	containerCGroup(ctx, rt, id)
	fmt.Println()
	containerImage(ctx, rt, id)
}

func containerStdio(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	stdio, err := c.Stdio(ctx)
	exitOnErr("Stdio", err)

	printJSON(stdio)
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

	entries := make([]any, 0, len(images))
	for _, img := range images {
		info, err := img.Info(ctx)
		if err != nil {
			entries = append(entries, map[string]any{
				"ref":   img.Ref(),
				"error": err.Error(),
			})
			continue
		}
		entries = append(entries, info)
	}

	printJSON(entries)
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

	if crioInfo != nil {
		printJSON(map[string]any{
			"info":         info,
			"crio_storage": crioInfo,
		})
	} else {
		printJSON(info)
	}
}

func imageConfig(ctx context.Context, rt runtime.Runtime, ref string) {
	img := mustGetImage(ctx, rt, ref)
	cfg, err := img.Config(ctx)
	exitOnErr("Config", err)

	printJSON(cfg)
}

func imageLayers(ctx context.Context, rt runtime.Runtime, ref, snapshotter string) {
	img := mustGetImage(ctx, rt, ref)
	layers, err := img.Layers(ctx, runtime.LayerQuery{Snapshotter: snapshotter})
	exitOnErr("Layers", err)

	printJSON(layers)
}

func runtimeInfo(ctx context.Context, rt runtime.Runtime) {
	if inspector, ok := rt.(runtimecrio.StoreIntrospector); ok {
		info, err := inspector.CRIOStoreInfo(ctx)
		exitOnErr("CRI-O store info", err)
		printJSON(info)
	} else {
		printJSON(map[string]any{
			"message": "No extended runtime introspection available for current backend.",
		})
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
		info, err := pod.Info(ctx)
		if err != nil {
			entries = append(entries, map[string]any{
				"uid":   pod.UID(),
				"error": err.Error(),
			})
			continue
		}
		containers, _ := pod.Containers(ctx)
		containerEntries := make([]any, 0, len(containers))
		for _, c := range containers {
			cInfo, err := c.Info(ctx)
			if err != nil {
				containerEntries = append(containerEntries, map[string]any{
					"id":    c.ID(),
					"error": err.Error(),
				})
				continue
			}
			containerEntries = append(containerEntries, cInfo)
		}
		entries = append(entries, map[string]any{
			"info":       info,
			"containers": containerEntries,
		})
	}

	printJSON(entries)
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

	containerEntries := make([]any, 0, len(containers))
	for _, c := range containers {
		cInfo, err := c.Info(ctx)
		if err != nil {
			containerEntries = append(containerEntries, map[string]any{
				"id":    c.ID(),
				"error": err.Error(),
			})
			continue
		}
		containerEntries = append(containerEntries, cInfo)
	}

	printJSON(map[string]any{
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

func printJSON(value any) {
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
