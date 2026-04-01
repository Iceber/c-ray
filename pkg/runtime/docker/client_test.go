package docker

import "testing"

func TestNormalizeDockerImageRef(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "short tag", input: "alpine:3.22", want: "docker.io/library/alpine:3.22"},
		{name: "short latest", input: "alpine", want: "docker.io/library/alpine:latest"},
		{name: "canonical already", input: "docker.io/library/alpine:3.22", want: "docker.io/library/alpine:3.22"},
		{name: "digest ref", input: "alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "docker.io/library/alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "image id unsupported", input: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDockerImageRef(tt.input)
			if got != tt.want {
				t.Fatalf("normalizeDockerImageRef(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectImageStoreMode(t *testing.T) {
	tests := []struct {
		name string
		info *daemonInfo
		want ImageStoreMode
	}{
		{
			name: "driver status containerd snapshotter",
			info: &daemonInfo{Driver: "overlayfs", DriverStatus: [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}},
			want: ImageStoreModeContainerd,
		},
		{
			name: "classic overlay2",
			info: &daemonInfo{Driver: "overlay2"},
			want: ImageStoreModeClassic,
		},
		{
			name: "overlayfs driver treated as containerd",
			info: &daemonInfo{Driver: "overlayfs"},
			want: ImageStoreModeContainerd,
		},
		{
			name: "nil daemon info",
			info: nil,
			want: ImageStoreModeUnknown,
		},
		{
			name: "unknown driver",
			info: &daemonInfo{Driver: "mysteryfs"},
			want: ImageStoreModeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectImageStoreMode(tt.info)
			if got != tt.want {
				t.Fatalf("detectImageStoreMode() = %v, want %v", got, tt.want)
			}
		})
	}
}