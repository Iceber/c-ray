package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"
)

//go:embed embedded/cray-linux
var embeddedBinary []byte

const (
	defaultHelperImage = "alpine:3.22"
	vmRoot             = "/vm"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// Precheck: verify Docker environment.
	if err := precheck(); err != nil {
		fmt.Fprintf(os.Stderr, "[cray-launcher] %v\n", err)
		os.Exit(1)
	}

	// Extract embedded Linux binary to temp directory.
	tempDir, err := os.MkdirTemp("", "cray-launcher-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cray-launcher] failed to create temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "cray-linux")
	if err := os.WriteFile(binaryPath, embeddedBinary, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[cray-launcher] failed to extract binary: %v\n", err)
		os.Exit(1)
	}

	// Unique path inside /vm for the chrooted binary.
	// NOTE: The VM's /tmp is mounted with noexec, so we stage in /root/.cray/ instead.
	vmBinaryPath := fmt.Sprintf("/root/.cray/cray-%s-%d", version, os.Getpid())

	// Detect whether stdin is a TTY.
	isTTY := term.IsTerminal(int(syscall.Stdin))

	// Helper image (overridable via CRAY_LAUNCHER_IMAGE).
	helperImage := defaultHelperImage
	if img := os.Getenv("CRAY_LAUNCHER_IMAGE"); img != "" {
		helperImage = img
	}

	// --- Build docker run arguments ---
	dockerArgs := []string{
		"run", "--rm",
		"--privileged",
		"--pid=host",
		"-v", "/:/vm",
		"-v", binaryPath + ":/work/cray:ro",
	}

	if isTTY {
		dockerArgs = append(dockerArgs, "-it")
	}

	// Pass through relevant environment variables.
	passEnv(&dockerArgs)

	dockerArgs = append(dockerArgs, helperImage)

	// --- Build the shell command executed inside the helper container ---
	//
	// Steps:
	//   1. Create the parent directory under /vm/root/.cray/
	//   2. Copy the embedded cray binary there (visible after chroot)
	//   3. chmod +x
	//   4. chroot /vm
	//   5. Execute cray with original user arguments
	//   6. Capture exit code and remove the temp binary
	//
	argsStr := joinShellArgs(os.Args[1:])

	shellCmd := fmt.Sprintf(
		"mkdir -p %s/root/.cray && cp /work/cray %s%s && chmod +x %s%s && chroot %s %s %s; ret=$?; rm -f %s%s; exit $ret",
		vmRoot,
		vmRoot, vmBinaryPath,
		vmRoot, vmBinaryPath,
		vmRoot, vmBinaryPath, argsStr,
		vmRoot, vmBinaryPath,
	)
	dockerArgs = append(dockerArgs, "sh", "-c", shellCmd)

	// Debug output.
	if os.Getenv("CRAY_LAUNCHER_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[cray-launcher] version=%s commit=%s\n", version, commit)
		fmt.Fprintf(os.Stderr, "[cray-launcher] docker %s\n", strings.Join(dockerArgs, " "))
	}

	// --- Execute ---
	os.Exit(run(dockerArgs))
}

// precheck verifies that the Docker environment is usable.
func precheck() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("'docker' CLI not found in PATH.\nDocker Desktop is required to run cray on macOS.")
	}

	cmd := exec.Command("docker", "info", "--format", "{{.OSType}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Docker daemon is not running or not accessible.\nPlease start Docker Desktop and try again.\n(%v)", err)
	}
	osType := strings.TrimSpace(string(output))
	if osType != "linux" {
		return fmt.Errorf("unexpected Docker OS type: %q (expected \"linux\")", osType)
	}
	return nil
}

// run starts the docker process, forwards signals, and returns the exit code.
func run(dockerArgs []string) int {
	cmd := exec.Command("docker", dockerArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Forward termination signals so Ctrl-C and SIGTERM propagate to the
	// helper container (which in turn propagates to the chrooted cray).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	err := cmd.Run()

	signal.Stop(sigCh)
	close(sigCh)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[cray-launcher] error: %v\n", err)
		return 1
	}
	return 0
}

// passEnv appends -e flags for environment variables that should be forwarded
// into the helper container (and into the chrooted cray via the shell command).
func passEnv(args *[]string) {
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch {
		case key == "TERM", key == "COLORTERM", key == "LANG":
			*args = append(*args, "-e", env)
		case strings.HasPrefix(key, "CRAY_") &&
			key != "CRAY_LAUNCHER_IMAGE" &&
			key != "CRAY_LAUNCHER_DEBUG":
			*args = append(*args, "-e", env)
		case strings.HasPrefix(key, "CONTAINERD_"):
			*args = append(*args, "-e", env)
		}
	}
}

// joinShellArgs quotes and joins arguments for safe use in sh -c.
func joinShellArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote wraps a value in single quotes, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
