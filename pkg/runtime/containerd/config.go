package containerd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/v2/client"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

const (
	defaultContainerdRoot   = "/var/lib/containerd"
	defaultContainerdState  = "/run/containerd"
	defaultContainerdConfig = "/etc/containerd/config.toml"
)

// containerdPaths holds the resolved root and state directories for containerd.
type containerdPaths struct {
	Root  string // data root (default: /var/lib/containerd)
	State string // state root (default: /run/containerd)
}

// containerdConfig is a minimal representation of the containerd config.toml,
// only capturing the fields we need.
type containerdConfig struct {
	Root  string `toml:"root"`
	State string `toml:"state"`
}

// resolveContainerdPaths discovers containerd's root and state directories by:
//  1. Calling the introspection API to get the server PID
//  2. Reading /proc/<pid>/cmdline to find the config file path
//  3. Parsing the config file for root and state fields
//
// Falls back to defaults when any step fails.
func resolveContainerdPaths(ctx context.Context, c *client.Client, procReader *sysinfo.ProcReader) containerdPaths {
	paths := containerdPaths{
		Root:  defaultContainerdRoot,
		State: defaultContainerdState,
	}

	configPath := resolveContainerdConfigPath(ctx, c, procReader)

	cfg, err := parseContainerdConfig(configPath)
	if err != nil {
		return paths
	}

	if cfg.Root != "" {
		paths.Root = cfg.Root
	}
	if cfg.State != "" {
		paths.State = cfg.State
	}

	return paths
}

// resolveContainerdConfigPath determines the config file path by inspecting
// the containerd process command line. Returns the default path if detection fails.
func resolveContainerdConfigPath(ctx context.Context, c *client.Client, procReader *sysinfo.ProcReader) string {
	if c == nil || procReader == nil {
		return defaultContainerdConfig
	}

	resp, err := c.IntrospectionService().Server(ctx)
	if err != nil || resp.Pid == 0 {
		return defaultContainerdConfig
	}

	args, err := procReader.ReadCmdlineRaw(int(resp.Pid))
	if err != nil {
		return defaultContainerdConfig
	}

	return parseConfigPathFromArgs(args)
}

// parseConfigPathFromArgs extracts the --config / -c flag value from a containerd
// command line. Returns the default config path if not found.
func parseConfigPathFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// --config /path/to/config.toml or --config=/path/to/config.toml
		if arg == "--config" || arg == "-c" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return defaultContainerdConfig
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config=")
		}
		if strings.HasPrefix(arg, "-c=") {
			return strings.TrimPrefix(arg, "-c=")
		}
	}

	return defaultContainerdConfig
}

// parseContainerdConfig reads and parses a containerd TOML config file.
func parseContainerdConfig(path string) (*containerdConfig, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("config path not absolute: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg containerdConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse containerd config %s: %w", path, err)
	}

	return &cfg, nil
}
