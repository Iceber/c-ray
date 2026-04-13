package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Bool pointer helper
// ---------------------------------------------------------------------------

func BoolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// Conmon cmdline parsing
// ---------------------------------------------------------------------------

// ConmonCmdlineFields holds fields extracted from a conmon command line.
type ConmonCmdlineFields struct {
	LogPath      string
	AttachSocket string
	Terminal     *bool
}

// ParseConmonCmdline extracts stdio-relevant flags from a conmon argument vector.
func ParseConmonCmdline(cmdline []string) *ConmonCmdlineFields {
	if len(cmdline) == 0 {
		return nil
	}
	f := &ConmonCmdlineFields{}
	found := false
	for i := 0; i < len(cmdline); i++ {
		arg := cmdline[i]
		switch {
		case (arg == "-l" || arg == "--log-path") && i+1 < len(cmdline):
			i++
			f.LogPath = cmdline[i]
			found = true
		case strings.HasPrefix(arg, "--log-path="):
			f.LogPath = strings.TrimPrefix(arg, "--log-path=")
			found = true
		case arg == "--attach-socket-path" && i+1 < len(cmdline):
			i++
			f.AttachSocket = cmdline[i]
			found = true
		case strings.HasPrefix(arg, "--attach-socket-path="):
			f.AttachSocket = strings.TrimPrefix(arg, "--attach-socket-path=")
			found = true
		case arg == "-t" || arg == "--terminal":
			f.Terminal = BoolPtr(true)
			found = true
		}
	}
	if !found {
		return nil
	}
	return f
}

// ---------------------------------------------------------------------------
// Userdata directory scanning (CRI-O bundle)
// ---------------------------------------------------------------------------

var stdioRelatedSuffixes = []string{
	"attach", "ctl", "control", "winsz", "resize", "log",
	"pidfile", "pid", "exit", "sock",
}

// ScanUserdataDir scans a CRI-O userdata directory for stdio/attach-related file paths.
func ScanUserdataDir(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "config.json" {
			continue
		}
		if isStdioRelatedFile(e.Name()) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths
}

func isStdioRelatedFile(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range stdioRelatedSuffixes {
		if strings.Contains(lower, suffix) {
			return true
		}
	}
	return false
}

// FindUserdataFile returns the full path of a file in dir if it exists, or "".
func FindUserdataFile(dir, filename string) string {
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, filename)
	if _, err := os.Lstat(path); err != nil {
		return ""
	}
	return path
}
