package runtime

// Mount describes one mount entry rendered by filesystem-related views.
type Mount struct {
	Source      string
	Destination string
	Type        string
	Options     []string
	HostPath    string
	LiveSource  string
	Origin      MountOrigin
	State       MountState
	Note        string
}

// MountOrigin identifies which subsystem produced a mount row.
type MountOrigin string

const (
	MountOriginCRI            MountOrigin = "cri"
	MountOriginRuntimeDefault MountOrigin = "runtime-default"
	MountOriginLiveExtra      MountOrigin = "live-extra"
)

// MountState records whether a mount came from declared config, live state, or both.
type MountState string

const (
	MountStateDeclaredLive MountState = "declared+live"
	MountStateDeclaredOnly MountState = "declared-only"
	MountStateLiveOnly     MountState = "live-only"
)
