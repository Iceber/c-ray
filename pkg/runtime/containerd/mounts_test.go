package containerd

import (
	"testing"

	"github.com/icebergu/c-ray/pkg/models"
	runtimecri "github.com/icebergu/c-ray/pkg/runtime/cri"
)

func TestMergeMountSourcesPrefersCRIAndKeepsLiveResidual(t *testing.T) {
	criMounts := &runtimecri.ContainerMounts{
		ConfigMounts: []*runtimecri.Mount{{
			ContainerPath: "/data",
			HostPath:      "/host/data",
			Readonly:      true,
		}},
		StatusMounts: []*runtimecri.Mount{{
			ContainerPath: "/data",
			HostPath:      "/host/data",
			Readonly:      true,
		}},
	}
	specMounts := []*models.Mount{
		{Source: "/host/data", Destination: "/data", Type: "bind", Options: []string{"rbind", "ro"}},
		{Source: "proc", Destination: "/proc", Type: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
	}
	liveMounts := []*models.Mount{
		{Source: "/host/data", Destination: "/data", Type: "bind", Options: []string{"ro"}},
		{Source: "proc", Destination: "/proc", Type: "proc", Options: []string{"rw"}},
		{Source: "overlay", Destination: "/", Type: "overlay", Options: []string{"rw"}},
	}

	mounts := mergeMountSources(criMounts, specMounts, liveMounts)
	if len(mounts) != 3 {
		t.Fatalf("mergeMountSources() len = %d, want 3", len(mounts))
	}

	byDestination := map[string]*models.Mount{}
	for _, mount := range mounts {
		byDestination[mount.Destination] = mount
	}

	dataMount := byDestination["/data"]
	if dataMount == nil {
		t.Fatal("expected CRI mount for /data")
	}
	if dataMount.Origin != models.MountOriginCRI {
		t.Fatalf("/data origin = %s, want %s", dataMount.Origin, models.MountOriginCRI)
	}
	if dataMount.State != models.MountStateDeclaredLive {
		t.Fatalf("/data state = %s, want %s", dataMount.State, models.MountStateDeclaredLive)
	}
	if dataMount.HostPath != "/host/data" {
		t.Fatalf("/data hostPath = %s, want /host/data", dataMount.HostPath)
	}

	procMount := byDestination["/proc"]
	if procMount == nil {
		t.Fatal("expected runtime default mount for /proc")
	}
	if procMount.Origin != models.MountOriginRuntimeDefault {
		t.Fatalf("/proc origin = %s, want %s", procMount.Origin, models.MountOriginRuntimeDefault)
	}

	rootMount := byDestination["/"]
	if rootMount == nil {
		t.Fatal("expected live residual mount for /")
	}
	if rootMount.Origin != models.MountOriginLiveExtra {
		t.Fatalf("/ origin = %s, want %s", rootMount.Origin, models.MountOriginLiveExtra)
	}
	if rootMount.State != models.MountStateLiveOnly {
		t.Fatalf("/ state = %s, want %s", rootMount.State, models.MountStateLiveOnly)
	}
}

func TestMergeMountSourcesMatchesVarRunAlias(t *testing.T) {
	criMounts := &runtimecri.ContainerMounts{
		ConfigMounts: []*runtimecri.Mount{{
			ContainerPath: "/var/run/secrets/kubernetes.io/serviceaccount",
			HostPath:      "/var/lib/kubelet/pods/test/volumes/kubernetes.io~projected/serviceaccount",
			Readonly:      true,
		}},
	}
	liveMounts := []*models.Mount{{
		Source:      "/var/lib/kubelet/pods/test/volumes/kubernetes.io~projected/serviceaccount",
		Destination: "/run/secrets/kubernetes.io/serviceaccount",
		Type:        "tmpfs",
		Options:     []string{"ro"},
	}}

	mounts := mergeMountSources(criMounts, nil, liveMounts)
	if len(mounts) != 1 {
		t.Fatalf("mergeMountSources() len = %d, want 1", len(mounts))
	}

	mount := mounts[0]
	if mount.Origin != models.MountOriginCRI {
		t.Fatalf("origin = %s, want %s", mount.Origin, models.MountOriginCRI)
	}
	if mount.State != models.MountStateDeclaredLive {
		t.Fatalf("state = %s, want %s", mount.State, models.MountStateDeclaredLive)
	}
	if mount.Destination != "/var/run/secrets/kubernetes.io/serviceaccount" {
		t.Fatalf("destination = %s, want CRI declared path", mount.Destination)
	}
	if mount.LiveSource != "/var/lib/kubelet/pods/test/volumes/kubernetes.io~projected/serviceaccount" {
		t.Fatalf("liveSource = %s, want propagated live source", mount.LiveSource)
	}
	if mount.Type != "tmpfs" {
		t.Fatalf("type = %s, want tmpfs from live mount", mount.Type)
	}
	if got := normalizeRunAlias("/var/run/secrets/kubernetes.io/serviceaccount"); got != "/run/secrets/kubernetes.io/serviceaccount" {
		t.Fatalf("normalizeRunAlias() = %s, want /run/secrets/kubernetes.io/serviceaccount", got)
	}
}
