package main

import (
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/icebergu/c-ray/pkg/runtime"
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
	version    = "dev"
	commit     = "unknown"
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
	rt := newRuntime(config)
	app := ui.NewApp(rt)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
