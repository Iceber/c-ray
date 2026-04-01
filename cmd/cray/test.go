package main

import (
	"context"
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
	fmt.Println("=== List Containers ===")
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d containers:\n\n", len(containers))
	for i, c := range containers {
		info, err := c.Info(ctx)
		if err != nil {
			fmt.Printf("[%d] %s  (info error: %v)\n", i+1, c.ID(), err)
			continue
		}
		fmt.Printf("[%d] Container:\n", i+1)
		fmt.Printf("  ID:        %s\n", shortID(info.ID))
		fmt.Printf("  Name:      %s\n", info.Name)
		fmt.Printf("  Image:     %s\n", info.Image)
		fmt.Printf("  Status:    %s\n", info.Status)
		fmt.Printf("  PID:       %d\n", info.PID)
		if info.PodName != "" {
			fmt.Printf("  Pod:       %s/%s\n", info.PodNamespace, info.PodName)
		}
		fmt.Println()
	}
}

func containerInfo(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	info, err := c.Info(ctx)
	exitOnErr("Info", err)

	fmt.Printf("=== Container Info: %s ===\n", shortID(id))
	fmt.Printf("ID:           %s\n", info.ID)
	fmt.Printf("Name:         %s\n", info.Name)
	fmt.Printf("Image:        %s\n", info.Image)
	fmt.Printf("Status:       %s\n", info.Status)
	fmt.Printf("PID:          %d\n", info.PID)
	fmt.Printf("CreatedAt:    %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))
	if info.PodName != "" {
		fmt.Printf("Pod:          %s/%s (uid=%s)\n", info.PodNamespace, info.PodName, info.PodUID)
	}
}

func containerConfig(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	cfg, err := c.Config(ctx)
	exitOnErr("Config", err)

	fmt.Printf("=== Container Config: %s ===\n", shortID(id))
	fmt.Printf("Image:          %s\n", cfg.ImageName)
	fmt.Printf("Snapshotter:    %s\n", cfg.Snapshotter)
	fmt.Printf("SnapshotKey:    %s\n", cfg.SnapshotKey)
	if cfg.CGroupPath != "" {
		fmt.Printf("CGroup Path:    %s\n", cfg.CGroupPath)
		fmt.Printf("CGroup Driver:  %s\n", cfg.CGroupDriver)
		fmt.Printf("CGroup Version: v%d\n", cfg.CGroupVersion)
	}
	if cfg.WritableLayerPath != "" {
		fmt.Printf("Writable Layer: %s\n", cfg.WritableLayerPath)
	}
	if len(cfg.Namespaces) > 0 {
		fmt.Println("\nNamespaces:")
		for ns, path := range cfg.Namespaces {
			if path != "" {
				fmt.Printf("  %-10s %s\n", ns, path)
			} else {
				fmt.Printf("  %-10s (new)\n", ns)
			}
		}
	}
	if len(cfg.Environment) > 0 {
		fmt.Printf("\nEnvironment (%d vars):\n", len(cfg.Environment))
		for _, e := range cfg.Environment {
			tag := ""
			if e.IsKubernetes {
				tag = " [k8s]"
			}
			fmt.Printf("  %s=%s%s\n", e.Key, truncate(e.Value, 60), tag)
		}
	}
}

func containerState(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	state, err := c.State(ctx)
	exitOnErr("State", err)

	fmt.Printf("=== Container State: %s ===\n", shortID(id))
	fmt.Printf("Status:        %s\n", state.Status)
	fmt.Printf("PID:           %d\n", state.PID)
	if state.PPID > 0 {
		fmt.Printf("PPID:          %d\n", state.PPID)
	}
	fmt.Printf("Process Count: %d\n", state.ProcessCount)
	if !state.StartedAt.IsZero() {
		fmt.Printf("Started:       %s\n", state.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if state.RestartCount != nil {
		fmt.Printf("Restarts:      %d\n", *state.RestartCount)
	}
	if state.ExitCode != nil {
		fmt.Printf("Exit Code:     %d\n", *state.ExitCode)
	}
	if state.ExitReason != "" {
		fmt.Printf("Exit Reason:   %s\n", state.ExitReason)
	}
	if !state.ExitedAt.IsZero() {
		fmt.Printf("Exited At:     %s\n", state.ExitedAt.Format("2006-01-02 15:04:05"))
	}
}

func containerRuntime(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	profile, err := c.Runtime(ctx)
	exitOnErr("Runtime", err)

	fmt.Printf("=== Container Runtime: %s ===\n", shortID(id))
	if profile.RootFSPath != "" {
		fmt.Printf("RootFS:          %s\n", profile.RootFSPath)
	}
	if oci := profile.OCI; oci != nil {
		fmt.Println("\n--- OCI ---")
		fmt.Printf("Runtime Name:    %s\n", oci.RuntimeName)
		fmt.Printf("Runtime Binary:  %s\n", oci.RuntimeBinary)
		fmt.Printf("Bundle Dir:      %s\n", oci.BundleDir)
		fmt.Printf("State Dir:       %s\n", oci.StateDir)
		if oci.ConfigPath != "" {
			fmt.Printf("Config Path:     %s\n", oci.ConfigPath)
		}
		if oci.SandboxID != "" {
			fmt.Printf("Sandbox ID:      %s\n", shortID(oci.SandboxID))
		}
	}
	if shim := profile.Shim; shim != nil {
		fmt.Println("\n--- Shim ---")
		if shim.BinaryPath != "" {
			fmt.Printf("Binary:          %s\n", shim.BinaryPath)
		}
		if shim.SocketAddress != "" {
			fmt.Printf("Socket:          %s\n", shim.SocketAddress)
		}
		if len(shim.Cmdline) > 0 {
			fmt.Printf("Cmdline:         %s\n", strings.Join(shim.Cmdline, " "))
		}
		if shim.SandboxBundleDir != "" {
			fmt.Printf("Sandbox Bundle:  %s\n", shim.SandboxBundleDir)
		}
	}
	if conmon := profile.Conmon; conmon != nil {
		fmt.Println("\n--- Conmon ---")
		fmt.Printf("PID:             %d\n", conmon.PID)
		if conmon.BinaryPath != "" {
			fmt.Printf("Binary:          %s\n", conmon.BinaryPath)
		}
		if len(conmon.Cmdline) > 0 {
			fmt.Printf("Cmdline:         %s\n", strings.Join(conmon.Cmdline, " "))
		}
		if conmon.LogDriver != "" {
			fmt.Printf("Log Driver:      %s\n", conmon.LogDriver)
		}
		if conmon.LogPath != "" {
			fmt.Printf("Log Path:        %s\n", conmon.LogPath)
		}
	}
}

func containerMounts(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	mounts, err := c.Mounts(ctx)
	exitOnErr("Mounts", err)

	fmt.Printf("=== Container Mounts: %s ===\n", shortID(id))
	fmt.Printf("Found %d mounts:\n\n", len(mounts))
	for i, m := range mounts {
		fmt.Printf("[%d] %s -> %s\n", i+1, m.Source, m.Destination)
		fmt.Printf("    Type: %s  Origin: %s  State: %s\n", m.Type, m.Origin, m.State)
		if len(m.Options) > 0 {
			fmt.Printf("    Options: %s\n", strings.Join(m.Options, ","))
		}
		if m.Note != "" {
			fmt.Printf("    Note: %s\n", m.Note)
		}
	}
}

func containerNetwork(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	net, err := c.Network(ctx)
	exitOnErr("Network", err)

	fmt.Printf("=== Container Network: %s ===\n", shortID(id))
	if net == nil || net.PodNetwork == nil {
		fmt.Println("No network info available.")
		return
	}

	pn := net.PodNetwork
	fmt.Printf("Sandbox ID:     %s\n", shortID(pn.SandboxID))
	fmt.Printf("Sandbox State:  %s\n", pn.SandboxState)
	fmt.Printf("Primary IP:     %s\n", pn.PrimaryIP)
	if len(pn.AdditionalIPs) > 0 {
		fmt.Printf("Additional IPs: %s\n", strings.Join(pn.AdditionalIPs, ", "))
	}
	fmt.Printf("Host Network:   %v\n", pn.HostNetwork)
	if pn.NetNSPath != "" {
		fmt.Printf("NetNS:          %s\n", pn.NetNSPath)
	}
	if pn.Hostname != "" {
		fmt.Printf("Hostname:       %s\n", pn.Hostname)
	}

	if len(pn.PortMappings) > 0 {
		fmt.Printf("\nPort Mappings (%d):\n", len(pn.PortMappings))
		for _, pm := range pn.PortMappings {
			fmt.Printf("  %s:%d -> %d/%s\n", pm.HostIP, pm.HostPort, pm.ContainerPort, pm.Protocol)
		}
	}

	if pn.CNI != nil && len(pn.CNI.Interfaces) > 0 {
		fmt.Printf("\nCNI Interfaces (%d):\n", len(pn.CNI.Interfaces))
		for _, iface := range pn.CNI.Interfaces {
			fmt.Printf("  %s (mac=%s)\n", iface.Name, iface.MAC)
			for _, addr := range iface.Addresses {
				fmt.Printf("    %s gw=%s (%s)\n", addr.CIDR, addr.Gateway, addr.Family)
			}
		}
	}

	if len(pn.ObservedInterfaces) > 0 {
		fmt.Printf("\nObserved Interfaces (%d):\n", len(pn.ObservedInterfaces))
		fmt.Printf("  %-12s %12s %12s %10s %10s\n", "IFACE", "RX BYTES", "TX BYTES", "RX PKT", "TX PKT")
		for _, s := range pn.ObservedInterfaces {
			fmt.Printf("  %-12s %12d %12d %10d %10d\n",
				s.Interface, s.RxBytes, s.TxBytes, s.RxPackets, s.TxPackets)
		}
	}

	if len(pn.Warnings) > 0 {
		fmt.Printf("\nWarnings:\n")
		for _, w := range pn.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func containerStorage(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	storage, err := c.Storage(ctx)
	exitOnErr("Storage", err)

	fmt.Printf("=== Container Storage: %s ===\n", shortID(id))
	if storage == nil {
		fmt.Println("No storage info available.")
		return
	}
	if storage.RWLayerPath != "" {
		fmt.Printf("RW Layer Path:  %s\n", storage.RWLayerPath)
	}

	rwStats, _ := c.RWLayerStats(ctx)
	if rwStats.RWLayerUsage > 0 {
		fmt.Printf("RW Usage:       %s (%d inodes)\n", formatBytes(rwStats.RWLayerUsage), rwStats.RWLayerInodes)
	}

	if len(storage.ReadOnlyLayers) > 0 {
		fmt.Printf("\nRead-Only Layers (%d):\n", len(storage.ReadOnlyLayers))
		for i := len(storage.ReadOnlyLayers) - 1; i >= 0; i-- {
			l := storage.ReadOnlyLayers[i]
			fmt.Printf("  [%d/%d] %s\n", l.Index, len(storage.ReadOnlyLayers), truncate(l.CompressedDigest, 24))
			fmt.Printf("         Size: %s  Disk: %s\n",
				formatContentSize(l.Size, l.CompressionType), formatBytes(l.UsageSize))
			if l.Path != "" {
				fmt.Printf("         Path: %s\n", l.Path)
			}
			if l.Crio != nil && len(l.Crio.Names) > 0 {
				fmt.Printf("         Names: %s\n", strings.Join(l.Crio.Names, ", "))
			}
		}
	}

	// CRI-O extended storage introspection (auto-detected).
	if inspector, ok := c.(runtimecrio.ContainerIntrospector); ok {
		crioInfo, err := inspector.CRIOContainerInfo(ctx)
		if err == nil {
			printCRIOContainerStorage(id, crioInfo)
		}
	}
}

func containerProcesses(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	procs, err := c.Processes(ctx)
	exitOnErr("Processes", err)

	fmt.Printf("=== Container Processes: %s ===\n", shortID(id))
	fmt.Printf("Found %d processes:\n\n", len(procs))
	for i, p := range procs {
		fmt.Printf("[%d] PID: %d  PPID: %d  State: %s\n", i+1, p.PID, p.PPID, p.State)
		fmt.Printf("    Command: %s\n", p.Command)
		if len(p.Args) > 0 {
			fmt.Printf("    Args: %s\n", strings.Join(p.Args, " "))
		}
	}

	stats, err := c.ProcessStats(ctx)
	if err == nil && stats != nil {
		fmt.Printf("\n--- Top Process ---\n")
		printProcessStats(stats, 0)
	}
}

func containerProcessStatsByPID(ctx context.Context, rt runtime.Runtime, id, pid string) {
	c := mustGetContainer(ctx, rt, id)
	stats, err := c.GetProcessStats(ctx, pid)
	exitOnErr("GetProcessStats", err)

	fmt.Printf("=== Process Stats: container=%s pid=%s ===\n", shortID(id), pid)
	if stats == nil {
		fmt.Println("No stats available.")
		return
	}
	printProcessStats(stats, 0)
}

func containerImage(ctx context.Context, rt runtime.Runtime, id string) {
	c := mustGetContainer(ctx, rt, id)
	img, err := c.Image(ctx)
	exitOnErr("Image", err)

	info, err := img.Info(ctx)
	exitOnErr("Image.Info", err)

	fmt.Printf("=== Container Image: %s ===\n", shortID(id))
	fmt.Printf("Ref:       %s\n", img.Ref())
	fmt.Printf("Name:      %s\n", info.Name)
	fmt.Printf("Digest:    %s\n", info.Digest)
	fmt.Printf("Size:      %s\n", formatBytes(info.Size))
	fmt.Printf("Created:   %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))
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
	fmt.Println("=== List Images ===")
	images, err := rt.ListImages(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d images:\n\n", len(images))
	for i, img := range images {
		info, err := img.Info(ctx)
		if err != nil {
			fmt.Printf("[%d] %s  (info error: %v)\n", i+1, img.Ref(), err)
			continue
		}
		fmt.Printf("[%d] Image:\n", i+1)
		fmt.Printf("  Name:    %s\n", info.Name)
		fmt.Printf("  Digest:  %s\n", truncate(info.Digest, 24))
		fmt.Printf("  Size:    %s\n", formatBytes(info.Size))
		fmt.Printf("  Created: %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()
	}
}

func imageInfo(ctx context.Context, rt runtime.Runtime, ref string) {
	img := mustGetImage(ctx, rt, ref)
	info, err := img.Info(ctx)
	exitOnErr("Info", err)

	fmt.Printf("=== Image Info: %s ===\n", ref)
	fmt.Printf("Name:      %s\n", info.Name)
	fmt.Printf("Digest:    %s\n", info.Digest)
	fmt.Printf("Size:      %s\n", formatBytes(info.Size))
	fmt.Printf("Created:   %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))

	// CRI-O extended image introspection (auto-detected).
	if inspector, ok := img.(runtimecrio.ImageIntrospector); ok {
		crioInfo, err := inspector.CRIOImageInfo(ctx)
		if err == nil {
			printCRIOImageStorage(ref, crioInfo)
		}
	}
}

func imageConfig(ctx context.Context, rt runtime.Runtime, ref string) {
	img := mustGetImage(ctx, rt, ref)
	cfg, err := img.Config(ctx)
	exitOnErr("Config", err)

	fmt.Printf("=== Image Config: %s ===\n", ref)
	if cfg == nil {
		fmt.Println("No config available.")
		return
	}
	fmt.Printf("Content Path:    %s\n", cfg.ContentPath)
	fmt.Printf("Target Media:    %s\n", cfg.TargetMediaType)
	fmt.Printf("Target Kind:     %s\n", cfg.TargetKind)
	fmt.Printf("Schema:          %s\n", cfg.Schema)
}

func imageLayers(ctx context.Context, rt runtime.Runtime, ref, snapshotter string) {
	img := mustGetImage(ctx, rt, ref)
	layers, err := img.Layers(ctx, runtime.LayerQuery{Snapshotter: snapshotter})
	exitOnErr("Layers", err)

	fmt.Printf("=== Image Layers: %s ===\n", ref)
	fmt.Printf("Found %d layers:\n\n", len(layers))

	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		fmt.Printf("[Layer %d/%d]\n", l.Index, len(layers))
		fmt.Printf("  Compressed:    %s\n", truncate(l.CompressedDigest, 24))
		fmt.Printf("  Uncompressed:  %s\n", truncate(l.UncompressedDigest, 24))
		fmt.Printf("  Size:          %s\n", formatContentSize(l.Size, l.CompressionType))
		if l.UsageSize > 0 {
			fmt.Printf("  Disk Usage:    %s (%d inodes)\n", formatBytes(l.UsageSize), l.UsageInodes)
		}
		if l.Path != "" {
			fmt.Printf("  Path:          %s\n", l.Path)
		}
		if l.Containerd != nil {
			if l.Containerd.ContentPath != "" {
				fmt.Printf("  Content Path:  %s\n", l.Containerd.ContentPath)
			}
			if l.Containerd.SnapshotKey != "" {
				fmt.Printf("  Snapshot Key:  %s\n", l.Containerd.SnapshotKey)
			}
		}
		if l.Crio != nil && len(l.Crio.Names) > 0 {
			fmt.Printf("  Names:         %s\n", strings.Join(l.Crio.Names, ", "))
		}
		fmt.Println()
	}
}

func runtimeInfo(ctx context.Context, rt runtime.Runtime) {
	if inspector, ok := rt.(runtimecrio.StoreIntrospector); ok {
		info, err := inspector.CRIOStoreInfo(ctx)
		exitOnErr("CRI-O store info", err)

		fmt.Println("=== Runtime Info (CRI-O) ===")
		fmt.Printf("Graph Root:       %s\n", info.GraphRoot)
		fmt.Printf("Run Root:         %s\n", info.RunRoot)
		fmt.Printf("Image Store:      %s\n", emptyDash(info.ImageStore))
		fmt.Printf("Driver:           %s\n", info.GraphDriverName)
		fmt.Printf("Transient Store:  %v\n", info.TransientStore)
		if len(info.GraphOptions) > 0 {
			fmt.Printf("Graph Options:    %s\n", strings.Join(info.GraphOptions, ", "))
		}
		if len(info.PullOptions) > 0 {
			fmt.Printf("Pull Options:     %s\n", formatStringMap(info.PullOptions))
		}
		if len(info.AdditionalImageStores) > 0 {
			fmt.Printf("Additional Image Stores: %s\n", strings.Join(info.AdditionalImageStores, ", "))
		}
		if len(info.AdditionalLayerStores) > 0 {
			fmt.Printf("Additional Layer Stores: %s\n", strings.Join(info.AdditionalLayerStores, ", "))
		}
		if len(info.DriverStatus) > 0 {
			fmt.Println("\nDriver Status:")
			for _, key := range sortedStringKeys(info.DriverStatus) {
				fmt.Printf("  %-20s %s\n", key, info.DriverStatus[key])
			}
		}
	} else {
		fmt.Println("=== Runtime Info ===")
		fmt.Println("No extended runtime introspection available for current backend.")
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
	fmt.Println("=== List Pods ===")
	pods, err := rt.ListPods(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d pods:\n\n", len(pods))
	for i, pod := range pods {
		info, err := pod.Info(ctx)
		if err != nil {
			fmt.Printf("[%d] %s  (info error: %v)\n", i+1, pod.UID(), err)
			continue
		}
		containers, _ := pod.Containers(ctx)
		fmt.Printf("[%d] Pod:\n", i+1)
		fmt.Printf("  Name:       %s\n", info.Name)
		fmt.Printf("  Namespace:  %s\n", info.Namespace)
		fmt.Printf("  UID:        %s\n", info.UID)
		fmt.Printf("  Containers: %d\n", len(containers))
		for j, c := range containers {
			cInfo, _ := c.Info(ctx)
			if cInfo != nil {
				fmt.Printf("    [%d] %s (%s)\n", j+1, cInfo.Name, cInfo.Status)
			}
		}
		fmt.Println()
	}
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

	fmt.Printf("=== Pod Info: %s ===\n", uid)
	fmt.Printf("Name:       %s\n", info.Name)
	fmt.Printf("Namespace:  %s\n", info.Namespace)
	fmt.Printf("UID:        %s\n", info.UID)

	containers, err := target.Containers(ctx)
	exitOnErr("Pod.Containers", err)

	fmt.Printf("Containers: %d\n\n", len(containers))
	for i, c := range containers {
		cInfo, err := c.Info(ctx)
		if err != nil {
			fmt.Printf("[%d] %s  (info error: %v)\n", i+1, c.ID(), err)
			continue
		}
		fmt.Printf("[%d] Container:\n", i+1)
		fmt.Printf("  ID:      %s\n", shortID(cInfo.ID))
		fmt.Printf("  Name:    %s\n", cInfo.Name)
		fmt.Printf("  Image:   %s\n", cInfo.Image)
		fmt.Printf("  Status:  %s\n", cInfo.Status)
		fmt.Printf("  PID:     %d\n", cInfo.PID)
		fmt.Println()
	}
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
