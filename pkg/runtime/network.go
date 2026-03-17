package runtime

// NetworkStats represents per-interface counters shown in process and network views.
type NetworkStats struct {
	Interface     string
	RxBytes       uint64
	TxBytes       uint64
	RxPackets     uint64
	TxPackets     uint64
	RxErrors      uint64
	TxErrors      uint64
	RxBytesPerSec float64
	TxBytesPerSec float64
}

// PortMapping represents a port mapping rendered by network inspection.
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

// DNSConfig contains DNS settings surfaced by runtime network metadata.
type DNSConfig struct {
	Domain   string
	Servers  []string
	Searches []string
	Options  []string
}

// CNIInterfaceAddress describes one CNI-assigned address.
type CNIInterfaceAddress struct {
	CIDR    string
	Gateway string
	Family  string
}

// CNIInterface describes one interface returned by normalized CNI inspection.
type CNIInterface struct {
	Name       string
	MAC        string
	Sandbox    string
	PciID      string
	SocketPath string
	Addresses  []*CNIInterfaceAddress
}

// CNIRoute describes one route returned by normalized CNI inspection.
type CNIRoute struct {
	Destination string
	Gateway     string
}

// CNIResultInfo contains normalized CNI data used by the network view.
type CNIResultInfo struct {
	Interfaces []*CNIInterface
	Routes     []*CNIRoute
	DNS        *DNSConfig
}

// PodNetworkInfo contains the pod-scoped network metadata currently rendered by the UI.
type PodNetworkInfo struct {
	SandboxID          string
	SandboxState       string
	PrimaryIP          string
	AdditionalIPs      []string
	HostNetwork        bool
	NamespaceMode      string
	NetNSPath          string
	Hostname           string
	DNS                *DNSConfig
	PortMappings       []*PortMapping
	RuntimeHandler     string
	RuntimeType        string
	CNI                *CNIResultInfo
	ObservedInterfaces []*NetworkStats
	Warnings           []string
}
