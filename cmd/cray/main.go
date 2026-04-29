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
	defaultTimeout = 30
)

var (
	socketPath string
	namespace  string
	timeout    int
)

func main() {
	flag.Usage = func() { printUsage(); os.Exit(0) }
	flag.StringVar(&socketPath, "socket", os.Getenv("CRAY_SOCKET"), "runtime socket path (auto-detected when empty)")
	flag.StringVar(&namespace, "namespace", os.Getenv("CONTAINERD_NAMESPACE"), "containerd namespace (auto: k8s.io for CRI-enabled containerd, default for plain containerd)")
	flag.IntVar(&timeout, "timeout", defaultTimeout, "connection timeout in seconds")
	flag.Parse()

	args := flag.Args()

	if len(args) > 0 {
		switch args[0] {
		case "container", "containers":
			runContainerCommand(args[1:])
			return
		case "image", "images":
			runImageCommand(args[1:])
			return
		case "pod", "pods":
			runPodCommand(args[1:])
			return
		case "runtime":
			runRuntimeCommand(args[1:])
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
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
			printUsage()
			os.Exit(1)
		}
	}

	// Check if running in interactive terminal
	if !isTerminal() {
		fmt.Fprintln(os.Stderr, "Error: cray TUI requires an interactive terminal.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "To use in non-interactive environments, use CLI commands:")
		fmt.Fprintln(os.Stderr, "  cray containers list")
		fmt.Fprintln(os.Stderr, "  cray container info <id>")
		fmt.Fprintln(os.Stderr, "  cray images list")
		fmt.Fprintln(os.Stderr, "  cray pods list")
		fmt.Fprintln(os.Stderr, "  cray runtime info")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "If running via docker exec, use the -it flags:")
		fmt.Fprintln(os.Stderr, "  docker exec -it <container> cray tui")
		os.Exit(1)
	}

	runTUI()
}

func printUsage() {
	fmt.Println("Usage: cray [flags] <command> [args]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -socket <path>      Runtime socket path (env: CRAY_SOCKET)")
	fmt.Println("  -namespace <name>   Containerd namespace (env: CONTAINERD_NAMESPACE)")
	fmt.Println("  -timeout <seconds>  Connection timeout (default: 30)")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  tui                         Start interactive TUI (default)")
	fmt.Println("  container(s) <action>       Container operations")
	fmt.Println("  image(s) <action>           Image operations")
	fmt.Println("  pod(s) <action>             Pod operations")
	fmt.Println("  runtime <action>            Runtime operations")
	fmt.Println("  help                        Show this help message")
	fmt.Println()
	fmt.Println("Run 'cray <command>' without arguments to see available actions.")
	fmt.Println("Both singular and plural resource names are accepted (e.g. container/containers).")
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
	rt, err := newRuntime(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	app := ui.NewApp(rt)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
