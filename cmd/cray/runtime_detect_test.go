package main

import "testing"

func TestDetectRuntimeAutoOrder(t *testing.T) {
	tests := []struct {
		name        string
		sockets     map[string]bool
		cri         map[string]bool
		docker      map[string]bool
		containerd  map[string]bool
		wantRuntime string
		wantSocket  string
		wantErr     bool
	}{
		{
			name: "prefer crio before all other runtimes",
			sockets: map[string]bool{
				crioSocketCandidates[0]:       true,
				containerdSocketCandidates[0]: true,
				dockerSocketCandidates[0]:     true,
			},
			cri: map[string]bool{
				crioSocketCandidates[0]:       true,
				containerdSocketCandidates[0]: true,
			},
			containerd: map[string]bool{
				containerdSocketCandidates[0]: true,
			},
			docker: map[string]bool{
				dockerSocketCandidates[0]: true,
			},
			wantRuntime: "crio",
			wantSocket:  crioSocketCandidates[0],
		},
		{
			name: "prefer cri enabled containerd before docker",
			sockets: map[string]bool{
				containerdSocketCandidates[0]: true,
				dockerSocketCandidates[0]:     true,
			},
			cri: map[string]bool{
				containerdSocketCandidates[0]: true,
			},
			containerd: map[string]bool{
				containerdSocketCandidates[0]: true,
			},
			docker: map[string]bool{
				dockerSocketCandidates[0]: true,
			},
			wantRuntime: "containerd",
			wantSocket:  containerdSocketCandidates[0],
		},
		{
			name: "prefer docker before plain containerd",
			sockets: map[string]bool{
				containerdSocketCandidates[0]: true,
				dockerSocketCandidates[0]:     true,
			},
			containerd: map[string]bool{
				containerdSocketCandidates[0]: true,
			},
			docker: map[string]bool{
				dockerSocketCandidates[0]: true,
			},
			wantRuntime: "docker",
			wantSocket:  dockerSocketCandidates[0],
		},
		{
			name:    "return error when auto detection finds nothing",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeName, socket, err := detectRuntimeWith(stubRuntimeDetector(tt.sockets, tt.cri, tt.docker, tt.containerd), "")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if runtimeName != tt.wantRuntime {
				t.Fatalf("runtime = %q, want %q", runtimeName, tt.wantRuntime)
			}
			if socket != tt.wantSocket {
				t.Fatalf("socket = %q, want %q", socket, tt.wantSocket)
			}
		})
	}
}

func TestDetectRuntimeExplicitSocket(t *testing.T) {
	tests := []struct {
		name        string
		socket      string
		cri         bool
		docker      bool
		containerd  bool
		wantRuntime string
	}{
		{
			name:        "explicit socket uses docker probe when available",
			socket:      "/tmp/custom.sock",
			docker:      true,
			wantRuntime: "docker",
		},
		{
			name:        "explicit socket with cri and containerd probes is containerd",
			socket:      "/tmp/custom.sock",
			cri:         true,
			containerd:  true,
			wantRuntime: "containerd",
		},
		{
			name:        "explicit socket with only cri probe is crio",
			socket:      "/tmp/custom.sock",
			cri:         true,
			wantRuntime: "crio",
		},
		{
			name:        "explicit path fallback still infers from name",
			socket:      "/tmp/docker-engine.sock",
			wantRuntime: "docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeName, socket, err := detectRuntimeWith(runtimeDetector{
				isSocket: func(path string) bool {
					return path == tt.socket
				},
				supportsCRI: func(path string) bool {
					return path == tt.socket && tt.cri
				},
				supportsDocker: func(path string) bool {
					return path == tt.socket && tt.docker
				},
				supportsContainerd: func(path string) bool {
					return path == tt.socket && tt.containerd
				},
			}, tt.socket)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if runtimeName != tt.wantRuntime {
				t.Fatalf("runtime = %q, want %q", runtimeName, tt.wantRuntime)
			}
			if socket != tt.socket {
				t.Fatalf("socket = %q, want %q", socket, tt.socket)
			}
		})
	}
}

func TestResolveContainerdNamespace(t *testing.T) {
	tests := []struct {
		name                string
		configuredNamespace string
		socketPath          string
		criEnabled          bool
		want                string
	}{
		{
			name:                "preserve explicit namespace",
			configuredNamespace: "custom-ns",
			socketPath:          "/run/containerd/containerd.sock",
			criEnabled:          true,
			want:                "custom-ns",
		},
		{
			name:       "default to k8s.io for cri enabled containerd",
			socketPath: "/run/containerd/containerd.sock",
			criEnabled: true,
			want:       defaultCRIContainerdNamespace,
		},
		{
			name:       "default to default for plain containerd",
			socketPath: "/run/containerd/containerd.sock",
			want:       defaultPlainContainerdNamespace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveContainerdNamespace(tt.configuredNamespace, tt.socketPath, func(path string) bool {
				return path == tt.socketPath && tt.criEnabled
			})
			if got != tt.want {
				t.Fatalf("namespace = %q, want %q", got, tt.want)
			}
		})
	}
}

func stubRuntimeDetector(sockets, cri, docker, containerd map[string]bool) runtimeDetector {
	return runtimeDetector{
		isSocket: func(path string) bool {
			return sockets[path]
		},
		supportsCRI: func(path string) bool {
			return cri[path]
		},
		supportsDocker: func(path string) bool {
			return docker[path]
		},
		supportsContainerd: func(path string) bool {
			return containerd[path]
		},
	}
}
