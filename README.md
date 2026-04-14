# c-ray

A container management TUI with deep runtime introspection capabilities. Supports **containerd**, **CRI-O**, **Docker** (Classic / containerd image store), and **Docker Desktop on macOS**.

## Screenshots

### Container Detail View
![Container Info](docs/container-info.png)

### Container Layers View
![Container Layers](docs/container-layers.png)

## Features

### Container Management
- **Container List**: ID, name, image, status, uptime, Pod association
- **Container Tree View**: Hierarchical display of Pod and container relationships
- **Container Detail Pages**:
  - Summary (status, PID, start time, restart count)
  - Environment variables (with Kubernetes annotations)
  - Process Top view (CPU, memory, RSS real-time monitoring)
  - Process tree (hierarchical process relationships)
  - Process list (detailed information)
  - Mount browser (with source tracking and status markers)
  - Image layers viewer (snapshots, compression info, disk usage)
  - Network info (CNI, DNS, port mappings, multiple IPs)
  - Runtime info (OCI config, runc state, CRI metadata)
  - Storage info (writable layer, snapshot usage)

### Image Management
- **Image List**: Name, digest, size, creation time
- **Image Details**: Tags, config, content path
- **Image Layer Analysis**:
  - Layer structure visualization
  - Compressed/uncompressed digests
  - Snapshot storage status
  - Disk usage statistics
  - Container layer overlay display

### Pod Management
- **Pod List**: Name, namespace, UID, container count
- **Pod Association**: Bidirectional navigation between containers and Pods

### Deep Runtime Introspection
- **CRI Metadata Integration**: Mounts, network, and state info from CRI
- **Mount Source Tracking**:
  - `cri`: CRI-configured mounts
  - `runtime-default`: Runtime default mounts
  - `live-extra`: Runtime dynamically added mounts
- **Mount Status**: `declared+live` / `declared-only` / `live-only`
- **CNI Network Details**: Interfaces, routes, DNS, port mappings
- **Process Resource Monitoring**: CPU usage, memory RSS, memory percentage

## Installation

Download the binary for your platform from the [latest release](https://github.com/icebergu/c-ray/releases/latest):

| Platform | File |
|----------|------|
| Linux amd64 | `cray-linux-amd64` |
| Linux arm64 | `cray-linux-arm64` |
| macOS Intel | `cray-darwin-amd64` |
| macOS Apple Silicon | `cray-darwin-arm64` |

```bash
# Replace <file> with the filename matching your platform
curl -fsSL -o /usr/local/bin/cray \
  https://github.com/icebergu/c-ray/releases/latest/download/<file>
chmod +x /usr/local/bin/cray
```

### Build from Source

```bash
git clone https://github.com/icebergu/c-ray.git && cd c-ray
make build
```

## Usage

### TUI Mode

```bash
# Start TUI (default, auto-detect runtime)
cray

# Or explicitly specify
cray tui
```

Other commands
```bash
# If auto-detection fails, specify socket explicitly
cray -socket /run/containerd/containerd.sock

# Specify containerd
cray -socket /run/containerd/containerd.sock -namespace k8s.io

# Common pattern for plain containerd
cray -socket /run/containerd/containerd.sock -namespace default

# Specify CRI-O
cray -socket /run/crio/crio.sock

# Specify Docker
cray -socket /var/run/docker.sock

# Full options
cray -socket /run/containerd/containerd.sock -namespace k8s.io -timeout 30
```

### CLI Mode

Suitable for non-interactive environments (CI/CD, remote execution).
Both singular and plural resource names are accepted (e.g. `container`/`containers`).

```bash
# Container operations
cray containers list
cray container info <id>
cray container config <id>
cray container state <id>
cray container runtime <id>
cray container mounts <id>
cray container network <id>
cray container storage <id>
cray container stdio <id>
cray container processes <id>
cray container process-stats <id> <pid>
cray container cgroup <id>
cray container image <id>
cray container all <id>

# Image operations
cray images list
cray image info <ref>
cray image config <ref>
cray image layers <ref> [snapshotter]

# Pod operations
cray pods list
cray pod info <uid>

# Runtime operations
cray runtime info
```

### TUI Keybindings

| Key | Action |
|-----|--------|
| `Up/Down` or `j/k` | Navigate list |
| `Enter` | Enter detail / Select |
| `Esc` or `q` | Go back / Exit |
| `Tab` | Switch view tabs |
| `1-9` | Quick switch detail page tabs |
| `r` | Refresh data |
| `/` | Search / Filter |

### Testing in Kind/Docker

```bash
# Use test script
./scripts/test-in-kind.sh

# Manually copy to kind node
GOOS=linux GOARCH=arm64 go build -o bin/cray-linux ./cmd/cray
cat bin/cray-linux | docker exec -i kind-control-plane bash -c "cat > /usr/local/bin/cray && chmod +x /usr/local/bin/cray"
docker exec kind-control-plane cray containers list
```

## Supported Runtimes

c-ray connects to multiple container runtimes through a unified abstraction layer. It automatically detects available sockets and selects the backend in this order: CRI-O, CRI-enabled containerd, Docker, and finally plain containerd. If all detection fails, it will error out and prompt you to use `-socket` or `CRAY_SOCKET` to specify explicitly.

| Runtime | Socket Path | Data Source | Notes |
|---------|-------------|-------------|-------|
| **containerd** | `/run/containerd/containerd.sock` | containerd API + CRI | Native containerd with namespace isolation, suitable for Kubernetes nodes |
| **CRI-O** | `/run/crio/crio.sock` | containers/storage + CRI | Uses containers/storage library for direct storage metadata access, CRI supplements runtime state |
| **Docker (Classic)** | `/var/run/docker.sock` | Docker Engine API | Traditional Docker graphdriver mode (overlay2, etc.) |
| **Docker (containerd)** | `/var/run/docker.sock` | Docker Engine API + containerd | Delegates image operations to containerd when containerd snapshotter is detected |
| **Docker Desktop (macOS)** | N/A (via launcher bridge) | Docker socket inside Docker Desktop VM | On macOS, uses a launcher to execute Linux binary via chroot in a Docker container |

## Contributing

Issues and Pull Requests are welcome!

## License

MIT License
