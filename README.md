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

## Supported Runtimes

c-ray connects to multiple container runtimes through a unified abstraction layer. It automatically detects available sockets and selects the backend in this order: CRI-O, CRI-enabled containerd, Docker, and finally plain containerd. If all detection fails, it will error out and prompt you to use `-socket` or `CRAY_SOCKET` to specify explicitly.

| Runtime | Socket Path | Data Source | Notes |
|---------|-------------|-------------|-------|
| **containerd** | `/run/containerd/containerd.sock` | containerd API + CRI | Native containerd with namespace isolation, suitable for Kubernetes nodes |
| **CRI-O** | `/run/crio/crio.sock` | containers/storage + CRI | Uses containers/storage library for direct storage metadata access, CRI supplements runtime state |
| **Docker (Classic)** | `/var/run/docker.sock` | Docker Engine API | Traditional Docker graphdriver mode (overlay2, etc.) |
| **Docker (containerd)** | `/var/run/docker.sock` | Docker Engine API + containerd | Delegates image operations to containerd when containerd snapshotter is detected |
| **Docker Desktop (macOS)** | N/A (via launcher bridge) | Docker socket inside Docker Desktop VM | On macOS, uses a launcher to execute Linux binary via chroot in a Docker container |

### Runtime Details

#### containerd

Native integration with containerd gRPC API, the first runtime supported by c-ray. Retrieves Pod, mount, and network metadata through Kubernetes CRI, combined with `/proc`, `/sys/fs/cgroup` and other system files for process and resource monitoring.

When `-namespace` or `CONTAINERD_NAMESPACE` is not explicitly specified, c-ray automatically selects the namespace based on detection results:

- CRI-enabled containerd: defaults to `k8s.io`
- Plain containerd: defaults to `default`

```bash
cray -socket /run/containerd/containerd.sock -namespace k8s.io
```

#### CRI-O

CRI-O mode uses [containers/storage](https://github.com/containers/storage) as the primary data source, directly reading storage backend to obtain container, image, and layer metadata without relying on CRI API for storage-level information. CRI API is only used to supplement runtime state (PID, lifecycle, labels, Pod association, network metadata).

Supports CRI-O's split store (`/var/lib/containers/storage` + `/run/containers/storage`) and transient store configurations.

```bash
cray -socket /run/crio/crio.sock
```

#### Docker

Manages containers through Docker Engine API. Automatically detects image storage mode on connection:

- **Classic**: Traditional graphdriver (overlay2, btrfs, etc.), image operations via Docker API
- **Containerd**: When `io.containerd.snapshotter.v1` is detected, image operations are automatically delegated to containerd backend (Docker Desktop 4.34+ / Docker Engine 29.0+ default)

```bash
cray -socket /var/run/docker.sock
```

#### Docker Desktop on macOS

macOS doesn't have native Linux container runtime, so c-ray provides a launcher wrapper:

1. The launcher is a Darwin native binary with an embedded statically-linked Linux c-ray binary
2. At runtime, it automatically starts a privileged Docker container that mounts the host `/` as `/vm`
3. Copies the embedded binary to the VM filesystem and executes it via `chroot /vm`
4. Inside the VM, it auto-detects available sockets using the unified strategy, typically resolving to Docker's `/var/run/docker.sock`

Prerequisite: Docker Desktop must be running.

```bash
cray   # On macOS, launcher is used automatically
```

## Installation

### Download from Release

```bash
# Linux AMD64
curl -L -o cray.tar.gz https://github.com/icebergu/c-ray/releases/latest/download/cray-linux-amd64.tar.gz
tar -xzf cray.tar.gz

# Linux ARM64
curl -L -o cray.tar.gz https://github.com/icebergu/c-ray/releases/latest/download/cray-linux-arm64.tar.gz
tar -xzf cray.tar.gz

# macOS Intel
curl -L -o cray.tar.gz https://github.com/icebergu/c-ray/releases/latest/download/cray-darwin-amd64.tar.gz
tar -xzf cray.tar.gz

# macOS Apple Silicon
curl -L -o cray.tar.gz https://github.com/icebergu/c-ray/releases/latest/download/cray-darwin-arm64.tar.gz
tar -xzf cray.tar.gz

# Move to PATH
chmod +x cray
sudo mv cray /usr/local/bin/
```

### Build from Source

```bash
# Clone the repository
git clone https://github.com/icebergu/c-ray.git
cd c-ray

# Build locally
make build

# Cross-compile for Linux
GOOS=linux GOARCH=arm64 go build -o bin/cray-linux ./cmd/cray
```

## Usage

### TUI Mode

```bash
# Start TUI (default, auto-detect runtime)
cray

# Or explicitly specify
cray tui

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

Suitable for non-interactive environments (CI/CD, remote execution):

```bash
# Container operations
cray test list-containers
cray test container-detail <container-id>
cray test container-processes <container-id>
cray test container-top <container-id>
cray test container-mounts <container-id>
cray test container-layers <container-id>
cray test crio-container-storage <container-id>

# Image operations
cray test list-images
cray test image-detail <image-ref>
cray test image-layers <image-id>
cray test crio-image-storage <image-ref>

# Pod operations
cray test list-pods

# CRI-O storage view
cray test crio-store-info
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
docker exec kind-control-plane cray test list-containers
```

## Project Structure

```
.
├── cmd/
│   ├── cray/               # Main entry point (Linux native)
│   └── cray-launcher/      # macOS launcher (embeds Linux binary, executes via chroot in Docker container)
├── pkg/
│   ├── models/             # Data models (container, image, pod, network)
│   ├── runtime/            # Runtime abstraction layer
│   │   ├── containerd/     # containerd implementation
│   │   ├── crio/           # CRI-O implementation (containers/storage + CRI)
│   │   ├── docker/         # Docker implementation (Engine API, auto-detects classic/containerd image store)
│   │   └── cri/            # CRI metadata client
│   ├── sysinfo/            # System information collection
│   │   ├── procfs/         # Process info
│   │   ├── cgroup/         # CGroup resources
│   │   └── mount/          # Mount point info
│   └── ui/                 # TUI interface layer
│       ├── app.go          # Application framework
│       └── views/          # View components
│           ├── container_detail.go   # Container detail page frame
│           ├── container_list.go     # Container list
│           ├── container_tree.go     # Container tree view
│           ├── detail_summary_view.go    # Detail summary
│           ├── image_layers_view.go      # Image layers
│           ├── image_list.go             # Image list
│           ├── mounts_view.go            # Mounts view
│           ├── network_info_view.go      # Network info
│           ├── pod_list.go               # Pod list
│           ├── process_summary_view.go   # Process summary
│           ├── process_tree_view.go      # Process tree
│           ├── processes_view.go         # Process list
│           ├── runtime_info_view.go      # Runtime info
│           ├── storage_view.go           # Storage info
│           └── top_view.go               # Top view
├── docs/                   # Technical documentation
│   ├── containerd/         # containerd related
│   ├── design/             # Design documents
│   └── runtime-spec/       # Runtime specs
└── scripts/                # Test scripts
```

## Tech Stack

- **Language**: Go 1.24.3+
- **TUI Framework**: [tview](https://github.com/rivo/tview)
- **Terminal Library**: [tcell](https://github.com/gdamore/tcell)
- **Container Runtimes**: [containerd](https://github.com/containerd/containerd) / [CRI-O](https://github.com/cri-o/cri-o) / [Docker](https://github.com/docker/docker)
- **Storage Library**: [containers/storage](https://github.com/containers/storage) (CRI-O mode)
- **CRI Interface**: Kubernetes CRI API

## Architecture

### Runtime Abstraction

```go
type Runtime interface {
    ListContainers(ctx context.Context) ([]*models.Container, error)
    GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error)
    GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error)
    GetContainerTop(ctx context.Context, id string) (*models.TopInfo, error)
    GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error)
    ListImages(ctx context.Context) ([]*models.Image, error)
    GetImageLayers(ctx context.Context, id, snapshotter, rwKey string) ([]*models.ImageLayer, error)
    ListPods(ctx context.Context) ([]*models.Pod, error)
    // ...
}
```

### CRI Metadata Enhancement

Retrieves Kubernetes-level metadata through a dedicated CRI client:

- **ContainerMounts**: CRI-configured mount point declarations
- **PodSandboxNetwork**: CNI results, DNS configuration, port mappings
- **ContainerStatus**: Restart count, exit status, environment variables

## Development Status

- [x] Project architecture design
- [x] containerd runtime integration
- [x] CRI-O runtime integration (containers/storage + CRI)
- [x] Docker runtime integration (Classic / containerd image store auto-detection)
- [x] Docker Desktop on macOS support (launcher + chroot)
- [x] Container list and details
- [x] Image management and layer analysis
- [x] Pod list and association
- [x] Process monitoring and resource statistics
- [x] CRI metadata integration
- [x] Network info display (CNI)
- [x] Mount source tracking
- [x] Storage and snapshot analysis
- [x] Multi-platform build and release

## Contributing

Issues and Pull Requests are welcome!

## License

MIT License
