package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/icebergu/c-ray/pkg/runtime"
	runtimecontainerd "github.com/icebergu/c-ray/pkg/runtime/containerd"
	runtimecrio "github.com/icebergu/c-ray/pkg/runtime/crio"
)

// Well-known runtime socket paths for auto-detection.
var knownSockets = []struct {
	path    string
	runtime string
}{
	{"/run/containerd/containerd.sock", "containerd"},
	{"/run/crio/crio.sock", "crio"},
	{"/var/run/containerd/containerd.sock", "containerd"},
	{"/var/run/crio/crio.sock", "crio"},
}

// detectRuntime determines the runtime type from the socket path string.
// It returns "containerd" or "crio" based on path content, falling back to
// probing known socket paths on the filesystem.
func detectRuntime(sock string) (string, string) {
	// If a socket path is explicitly provided, infer runtime from it.
	if sock != "" {
		if strings.Contains(sock, "crio") {
			return "crio", sock
		}
		if strings.Contains(sock, "containerd") {
			return "containerd", sock
		}
		// Path given but unrecognised — try it as containerd (legacy default).
		return "containerd", sock
	}

	// No explicit path: probe well-known locations.
	for _, ks := range knownSockets {
		if fi, err := os.Stat(ks.path); err == nil && fi.Mode().Type() == os.ModeSocket {
			return ks.runtime, ks.path
		}
	}

	// Nothing found — fall back to the compiled-in default.
	return "containerd", defaultSocketPath
}

// newRuntime creates a runtime.Runtime using the detected runtime type.
func newRuntime(config *runtime.Config) runtime.Runtime {
	runtimeType, resolvedSocket := detectRuntime(config.SocketPath)
	config.SocketPath = resolvedSocket

	switch runtimeType {
	case "crio":
		fmt.Fprintf(os.Stderr, "[runtime] detected CRI-O (socket: %s)\n", resolvedSocket)
		return runtimecrio.New(config)
	default:
		fmt.Fprintf(os.Stderr, "[runtime] detected containerd (socket: %s)\n", resolvedSocket)
		return runtimecontainerd.New(config)
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
		fmt.Println("    container-processes <id>            Show container processes")
		fmt.Println("    container-all <id>                 Show all container details")
		fmt.Println("\n  Images:")
		fmt.Println("    list-images                        List all images")
		fmt.Println("    image-info <ref>                   Show image info")
		fmt.Println("    image-config <ref>                 Show image config")
		fmt.Println("    image-layers <ref> [snapshotter]   Show image layers")
		fmt.Println("\n  Pods:")
		fmt.Println("    list-pods                          List all pods")
		os.Exit(1)
	}

	command := args[0]

	config := &runtime.Config{
		SocketPath: socketPath,
		Namespace:  namespace,
		Timeout:    timeout,
	}

	rt := newRuntime(config)

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
		if l.ContentPath != "" {
			fmt.Printf("  Content Path:  %s\n", l.ContentPath)
		}
		fmt.Println()
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
