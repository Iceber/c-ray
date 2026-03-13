package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/containerd"
	"github.com/icebergu/c-ray/pkg/ui"
	"golang.org/x/term"
)

// isTerminal checks if we have an interactive terminal
func isTerminal() bool {
	// Check both stdin and stdout - either being a terminal is sufficient
	if !term.IsTerminal(int(syscall.Stdin)) && !term.IsTerminal(int(syscall.Stdout)) {
		return false
	}

	// Also check if /dev/tty is accessible (required by tcell)
	// In some SSH/remote environments, stdin is a terminal but /dev/tty is not available
	if _, err := os.Stat("/dev/tty"); err != nil {
		return false
	}
	// Try to open /dev/tty to verify it's actually usable
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	f.Close()

	return true
}

const (
	defaultSocketPath = "/run/containerd/containerd.sock"
	defaultNamespace  = "k8s.io"
	defaultTimeout    = 30
)

var (
	socketPath string
	namespace  string
	timeout    int
)

func main() {
	flag.StringVar(&socketPath, "socket", getEnvOrDefault("CONTAINERD_SOCKET", defaultSocketPath), "containerd socket path")
	flag.StringVar(&namespace, "namespace", getEnvOrDefault("CONTAINERD_NAMESPACE", defaultNamespace), "containerd namespace")
	flag.IntVar(&timeout, "timeout", defaultTimeout, "connection timeout in seconds")
	flag.Parse()

	args := flag.Args()

	if len(args) > 0 {
		switch args[0] {
		case "test":
			runTests(args[1:])
			return
		case "tui":
			if len(args) > 1 {
				fmt.Fprintln(os.Stderr, "Usage: cray tui")
				os.Exit(1)
			}
		case "help", "-h", "--help":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", args[0])
			printUsage()
			os.Exit(1)
		}
	}

	// Check if running in interactive terminal
	if !isTerminal() {
		fmt.Fprintln(os.Stderr, "Error: cray TUI requires an interactive terminal.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "To use in non-interactive environments:")
		fmt.Fprintln(os.Stderr, "  1. Use 'cray test <command>' for CLI mode:")
		fmt.Fprintln(os.Stderr, "     cray test list-containers")
		fmt.Fprintln(os.Stderr, "     cray test list-images")
		fmt.Fprintln(os.Stderr, "     cray test list-pods")
		fmt.Fprintln(os.Stderr, "     cray test container-detail <id>")
		fmt.Fprintln(os.Stderr, "     cray test container-processes <id>")
		fmt.Fprintln(os.Stderr, "     cray test container-top <id>")
		fmt.Fprintln(os.Stderr, "     cray test container-mounts <id>")
		fmt.Fprintln(os.Stderr, "     cray test image-detail <ref>")
		fmt.Fprintln(os.Stderr, "     cray test image-layers <image-id> [snapshotter] [rw-snapshot-key]")
		fmt.Fprintln(os.Stderr, "     cray test container-layers <container-id>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  2. If running via docker exec, use the -it flags:")
		fmt.Fprintln(os.Stderr, "     docker exec -it <container> cray tui")
		os.Exit(1)
	}

	runTUI()
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  cray tui")
	fmt.Println("  cray test <command>")
}

func runTests(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: cray test <command>")
		fmt.Println("\nAvailable commands:")
		fmt.Println("  Containers:")
		fmt.Println("    list-containers                    List all containers")
		fmt.Println("    container-detail <id>              Show container details")
		fmt.Println("    container-processes <id>           List container processes")
		fmt.Println("    container-top <id>                 Show top-like process info")
		fmt.Println("    container-mounts <id>              List container mounts")
		fmt.Println("\n  Images:")
		fmt.Println("    list-images                        List all images")
		fmt.Println("    image-detail <ref>                 Show image details")
		fmt.Println("    image-layers <id> [snapshotter]    Show image layers")
		fmt.Println("    container-layers <id>              Show container's image layers (auto-detect snapshotter)")
		fmt.Println("\n  Pods:")
		fmt.Println("    list-pods                          List all pods")
		os.Exit(1)
	}

	command := args[0]

	// Create runtime configuration
	config := newConfig()

	// Create containerd runtime
	rt := containerd.NewContainerdRuntime(config)

	// Connect
	ctx := context.Background()
	if err := rt.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to containerd: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	// Execute command
	switch command {
	case "list-containers":
		testListContainers(ctx, rt)
	case "list-images":
		testListImages(ctx, rt)
	case "list-pods":
		testListPods(ctx, rt)
	case "container-detail":
		if len(args) < 2 {
			fmt.Println("Usage: cray test container-detail <container-id>")
			os.Exit(1)
		}
		testContainerDetail(ctx, rt, args[1])
	case "container-processes":
		if len(args) < 2 {
			fmt.Println("Usage: cray test container-processes <container-id>")
			os.Exit(1)
		}
		testContainerProcesses(ctx, rt, args[1])
	case "container-top":
		if len(args) < 2 {
			fmt.Println("Usage: cray test container-top <container-id>")
			os.Exit(1)
		}
		testContainerTop(ctx, rt, args[1])
	case "container-mounts":
		if len(args) < 2 {
			fmt.Println("Usage: cray test container-mounts <container-id>")
			os.Exit(1)
		}
		testContainerMounts(ctx, rt, args[1])
	case "image-detail":
		if len(args) < 2 {
			fmt.Println("Usage: cray test image-detail <image-ref>")
			os.Exit(1)
		}
		testImageDetail(ctx, rt, args[1])
	case "image-layers":
		if len(args) < 2 {
			fmt.Println("Usage: cray test image-layers <image-id> [snapshotter] [rw-snapshot-key]")
			os.Exit(1)
		}
		snapshotter := ""
		rwKey := ""
		if len(args) >= 3 {
			snapshotter = args[2]
		}
		if len(args) >= 4 {
			rwKey = args[3]
		}
		testImageLayers(ctx, rt, args[1], snapshotter, rwKey)
	case "container-layers":
		if len(args) < 2 {
			fmt.Println("Usage: cray test container-layers <container-id>")
			os.Exit(1)
		}
		testContainerLayers(ctx, rt, args[1])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func testListContainers(ctx context.Context, rt runtime.Runtime) {
	fmt.Println("=== Listing Containers ===")
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d containers:\n\n", len(containers))
	for i, c := range containers {
		fmt.Printf("[%d] Container:\n", i+1)
		fmt.Printf("  ID:        %s\n", c.ID[:12])
		fmt.Printf("  Name:      %s\n", c.Name)
		fmt.Printf("  Image:     %s\n", c.Image)
		fmt.Printf("  Status:    %s\n", c.Status)
		fmt.Printf("  PID:       %d\n", c.PID)
		if c.PodName != "" {
			fmt.Printf("  Pod:       %s/%s\n", c.PodNamespace, c.PodName)
		}
		fmt.Println()
	}
}

func testListImages(ctx context.Context, rt runtime.Runtime) {
	fmt.Println("=== Listing Images ===")
	images, err := rt.ListImages(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d images:\n\n", len(images))
	for i, img := range images {
		fmt.Printf("[%d] Image:\n", i+1)
		fmt.Printf("  Name:      %s\n", img.Name)
		fmt.Printf("  Digest:    %s\n", img.Digest[:20]+"...")
		fmt.Printf("  Size:      %.2f MB\n", float64(img.Size)/(1024*1024))
		fmt.Printf("  Created:   %s\n", img.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()
	}
}

func testListPods(ctx context.Context, rt runtime.Runtime) {
	fmt.Println("=== Listing Pods ===")
	pods, err := rt.ListPods(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d pods:\n\n", len(pods))
	for i, pod := range pods {
		fmt.Printf("[%d] Pod:\n", i+1)
		fmt.Printf("  Name:       %s\n", pod.Name)
		fmt.Printf("  Namespace:  %s\n", pod.Namespace)
		fmt.Printf("  UID:        %s\n", pod.UID)
		fmt.Printf("  Containers: %d\n", len(pod.Containers))
		for j, c := range pod.Containers {
			fmt.Printf("    [%d] %s (%s)\n", j+1, c.Name, c.Status)
		}
		fmt.Println()
	}
}

func testContainerDetail(ctx context.Context, rt runtime.Runtime, containerID string) {
	fmt.Printf("=== Container Detail: %s ===\n", containerID)
	detail, err := rt.GetContainerDetail(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	storageDetail, err := rt.GetContainerStorageInfo(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n--- Basic Info ---")
	fmt.Printf("ID:            %s\n", detail.ID)
	fmt.Printf("Name:          %s\n", detail.Name)
	fmt.Printf("Image:         %s\n", detail.Image)
	fmt.Printf("Status:        %s\n", detail.Status)
	fmt.Printf("PID:           %d\n", detail.PID)

	if detail.PodName != "" {
		fmt.Println("\n--- Pod Info ---")
		fmt.Printf("Pod Name:      %s\n", detail.PodName)
		fmt.Printf("Namespace:     %s\n", detail.PodNamespace)
		fmt.Printf("Pod UID:       %s\n", detail.PodUID)
	}

	fmt.Println("\n--- Process Info ---")
	fmt.Printf("Process Count: %d\n", detail.ProcessCount)

	if detail.CGroupLimits != nil {
		fmt.Println("\n--- CGroup Limits ---")
		fmt.Printf("CGroup Version: v%d\n", detail.CGroupVersion)
		fmt.Printf("CGroup Path:    %s\n", detail.CGroupPath)
		if detail.CGroupLimits.CPUQuota > 0 {
			fmt.Printf("CPU Quota:      %d us\n", detail.CGroupLimits.CPUQuota)
			fmt.Printf("CPU Period:     %d us\n", detail.CGroupLimits.CPUPeriod)
		}
		if detail.CGroupLimits.CPUShares > 0 {
			fmt.Printf("CPU Shares:     %d\n", detail.CGroupLimits.CPUShares)
		}
		if detail.CGroupLimits.MemoryLimit > 0 {
			fmt.Printf("Memory Limit:   %.2f MB\n", float64(detail.CGroupLimits.MemoryLimit)/(1024*1024))
		}
		if detail.CGroupLimits.MemoryUsage > 0 {
			fmt.Printf("Memory Usage:   %.2f MB\n", float64(detail.CGroupLimits.MemoryUsage)/(1024*1024))
		}
		if detail.CGroupLimits.PidsLimit > 0 {
			fmt.Printf("PIDs Limit:     %d\n", detail.CGroupLimits.PidsLimit)
		}
		if detail.CGroupLimits.PidsCurrent > 0 {
			fmt.Printf("PIDs Current:   %d\n", detail.CGroupLimits.PidsCurrent)
		}
	}

	fmt.Println("\n--- Mount Info ---")
	fmt.Printf("Mount Count:   %d\n", storageDetail.MountCount)
	if len(storageDetail.Mounts) > 0 {
		fmt.Println("\nFirst 5 mounts:")
		for i, m := range storageDetail.Mounts {
			if i >= 5 {
				break
			}
			fmt.Printf("  [%d] %s -> %s (%s)\n", i+1, m.Source, m.Destination, m.Type)
		}
	}

	fmt.Println("\n--- Image Info ---")
	fmt.Printf("Image Name:    %s\n", detail.ImageName)
	if storageDetail.WritableLayerPath != "" {
		fmt.Printf("Writable Layer: %s\n", storageDetail.WritableLayerPath)
	}
}

func testContainerProcesses(ctx context.Context, rt runtime.Runtime, containerID string) {
	fmt.Printf("=== Container Processes: %s ===\n", containerID)
	processes, err := rt.GetContainerProcesses(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d processes:\n\n", len(processes))
	for i, p := range processes {
		fmt.Printf("[%d] PID: %d, PPID: %d, State: %s\n", i+1, p.PID, p.PPID, p.State)
		fmt.Printf("    Command: %s\n", p.Command)
		if len(p.Args) > 0 {
			fmt.Printf("    Args: %v\n", p.Args)
		}
		if p.CPUPercent > 0 {
			fmt.Printf("    CPU: %.2f%%\n", p.CPUPercent)
		}
		if p.MemoryRSS > 0 {
			fmt.Printf("    Memory RSS: %.2f MB\n", float64(p.MemoryRSS)/(1024*1024))
		}
		fmt.Println()
	}
}

func testContainerTop(ctx context.Context, rt runtime.Runtime, containerID string) {
	fmt.Printf("=== Container Top: %s ===\n", containerID)
	top, err := rt.GetContainerTop(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Timestamp: %d\n", top.Timestamp)
	fmt.Printf("Processes: %d\n\n", len(top.Processes))

	// Print header
	fmt.Printf("%-8s %-8s %-10s %-10s %-10s %s\n", "PID", "PPID", "CPU%", "MEM%", "RSS(MB)", "COMMAND")
	fmt.Println(string(make([]byte, 80)))

	for _, p := range top.Processes {
		rssMB := float64(p.MemoryRSS) / (1024 * 1024)
		fmt.Printf("%-8d %-8d %-10.2f %-10.2f %-10.2f %s\n",
			p.PID, p.PPID, p.CPUPercent, p.MemoryPercent, rssMB, p.Command)
	}
}

func testContainerMounts(ctx context.Context, rt runtime.Runtime, containerID string) {
	fmt.Printf("=== Container Mounts: %s ===\n", containerID)
	mounts, err := rt.GetContainerMounts(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d mounts:\n\n", len(mounts))
	for i, m := range mounts {
		fmt.Printf("[%d] %s -> %s (%s)\n", i+1, m.Source, m.Destination, m.Type)
		if len(m.Options) > 0 {
			fmt.Printf("    Options: %v\n", m.Options)
		}
	}
}

func testImageDetail(ctx context.Context, rt runtime.Runtime, imageRef string) {
	fmt.Printf("=== Image Detail: %s ===\n", imageRef)
	img, err := rt.GetImage(ctx, imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n--- Basic Info ---")
	fmt.Printf("Name:      %s\n", img.Name)
	fmt.Printf("Digest:    %s\n", img.Digest)
	fmt.Printf("Size:      %.2f MB\n", float64(img.Size)/(1024*1024))
	fmt.Printf("Created:   %s\n", img.CreatedAt.Format("2006-01-02 15:04:05"))

	if len(img.Labels) > 0 {
		fmt.Println("\n--- Labels ---")
		for k, v := range img.Labels {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}

	// Also get config info
	configInfo, err := rt.GetImageConfigInfo(ctx, imageRef)
	if err == nil && configInfo != nil {
		fmt.Println("\n--- Config Info ---")
		fmt.Printf("Digest:      %s\n", configInfo.Digest)
		fmt.Printf("Size:        %s\n", formatBytes(configInfo.Size))
		fmt.Printf("Content Path: %s\n", configInfo.ContentPath)
	}
}

func testImageLayers(ctx context.Context, rt runtime.Runtime, imageID, snapshotter, rwSnapshotKey string) {
	fmt.Printf("=== Image Layers: %s ===\n", imageID)
	if snapshotter != "" {
		fmt.Printf("Snapshotter: %s\n", snapshotter)
	}
	if rwSnapshotKey != "" {
		fmt.Printf("RW Snapshot Key: %s\n", rwSnapshotKey)
	}
	fmt.Println()

	layers, err := rt.GetImageLayers(ctx, imageID, snapshotter, rwSnapshotKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d layers:\n\n", len(layers))

	// Display from top to base (reverse order)
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		fmt.Printf("[Layer %d/%d]\n", layer.Index, len(layers))
		fmt.Printf("  Snapshot Key:    %s\n", layer.SnapshotKey)
		fmt.Printf("  Compressed:      %s\n", truncateDigest(layer.CompressedDigest, 19))
		fmt.Printf("  Uncompressed:    %s\n", truncateDigest(layer.UncompressedDigest, 19))
		fmt.Printf("  Content Size:    %s\n", formatContentSize(layer.Size, layer.CompressionType))
		if layer.SnapshotExists && layer.UsageSize > 0 {
			fmt.Printf("  Disk Usage:      %s (%d inodes)\n", formatBytes(layer.UsageSize), layer.UsageInodes)
		}
		fmt.Printf("  Unpacked:        %v\n", layer.SnapshotExists)
		if layer.SnapshotPath != "" {
			fmt.Printf("  Snapshot Path:   %s\n", layer.SnapshotPath)
		}
		if layer.ContentPath != "" {
			fmt.Printf("  Content Path:    %s\n", layer.ContentPath)
		}
		fmt.Println()
	}
}

func testContainerLayers(ctx context.Context, rt runtime.Runtime, containerID string) {
	fmt.Printf("=== Container Layers: %s ===\n\n", containerID)

	// Get container detail to extract image, snapshotter and snapshot key
	detail, err := rt.GetContainerStorageInfo(ctx, containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting container detail: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Container:   %s\n", detail.Name)
	fmt.Printf("Image:       %s\n", detail.Image)
	fmt.Printf("Snapshotter: %s\n", detail.Snapshotter)
	fmt.Printf("RW Key:      %s\n", detail.SnapshotKey)
	if detail.RWLayerUsage > 0 {
		fmt.Printf("RW Usage:    %s (%d inodes)\n", formatBytes(detail.RWLayerUsage), detail.RWLayerInodes)
	}
	fmt.Println()

	if detail.Image == "" {
		fmt.Fprintln(os.Stderr, "Error: container has no image")
		os.Exit(1)
	}

	// Get image layers with auto-detected snapshotter and rw key
	layers, err := rt.GetImageLayers(ctx, detail.Image, detail.Snapshotter, detail.SnapshotKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image layers: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d layers:\n\n", len(layers))

	// Display from top to base (reverse order)
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		fmt.Printf("[Layer %d/%d]\n", layer.Index, len(layers))
		fmt.Printf("  Snapshot Key:    %s\n", layer.SnapshotKey)
		fmt.Printf("  Compressed:      %s\n", truncateDigest(layer.CompressedDigest, 19))
		fmt.Printf("  Uncompressed:    %s\n", truncateDigest(layer.UncompressedDigest, 19))
		fmt.Printf("  Content Size:    %s\n", formatContentSize(layer.Size, layer.CompressionType))
		if layer.SnapshotExists && layer.UsageSize > 0 {
			fmt.Printf("  Disk Usage:      %s (%d inodes)\n", formatBytes(layer.UsageSize), layer.UsageInodes)
		}
		fmt.Printf("  Unpacked:        %v\n", layer.SnapshotExists)
		if layer.SnapshotPath != "" {
			fmt.Printf("  Snapshot Path:   %s\n", layer.SnapshotPath)
		} else if layer.SnapshotExists {
			fmt.Printf("  Snapshot Path:   (not available)\n")
		}
		if layer.ContentPath != "" {
			fmt.Printf("  Content Path:    %s\n", layer.ContentPath)
		}
		fmt.Println()
	}
}

// formatContentSize formats the content size with compression type
// Format: <size>(<compression>) or <size> if no compression
func formatContentSize(size int64, compression string) string {
	sizeStr := formatBytes(size)
	if compression == "" {
		return sizeStr + "(-)"
	}
	return fmt.Sprintf("%s(%s)", sizeStr, compression)
}

// formatBytes formats bytes to human-readable string
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// truncateDigest truncates a digest string for display
func truncateDigest(digest string, length int) string {
	if len(digest) <= length {
		return digest
	}
	return digest[:length]
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func newConfig() *runtime.Config {
	return &runtime.Config{
		SocketPath: socketPath,
		Namespace:  namespace,
		Timeout:    timeout,
	}
}

func runTUI() {
	// Check TERM environment variable
	termEnv := os.Getenv("TERM")
	if termEnv == "" {
		fmt.Fprintln(os.Stderr, "Warning: TERM environment variable not set, setting to 'xterm'")
		os.Setenv("TERM", "xterm")
	}

	config := newConfig()
	rt := containerd.NewContainerdRuntime(config)
	app := ui.NewApp(rt)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
