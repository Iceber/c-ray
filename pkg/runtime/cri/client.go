package cri

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Mount captures the CRI mount fields needed by the runtime mount merger.
type Mount struct {
	ContainerPath     string
	HostPath          string
	Readonly          bool
	SelinuxRelabel    bool
	RecursiveReadOnly bool
	Propagation       runtimeapi.MountPropagation
	Image             string
}

// ContainerMounts contains CRI-declared mounts and the CRI status mirror.
type ContainerMounts struct {
	ConfigMounts []*Mount
	StatusMounts []*Mount
}

// ContainerEnv captures one container env var from CRI config.
type ContainerEnv struct {
	Key   string
	Value string
}

// ContainerStatusInfo captures CRI container status fields needed by the TUI.
type ContainerStatusInfo struct {
	StartedAt    time.Time
	FinishedAt   time.Time
	ExitCode     *int32
	Reason       string
	RestartCount *uint32
	PIDMode      string
	SharedPID    *bool
	Envs         []ContainerEnv
}

// PortMapping captures CRI PodSandbox port mappings used by runtime inspection.
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

// DNSConfig captures CRI sandbox DNS settings relevant to network inspection.
type DNSConfig struct {
	Domain   string
	Servers  []string
	Searches []string
	Options  []string
}

// CNIInterfaceAddress describes one CNI-assigned address in CIDR form.
type CNIInterfaceAddress struct {
	CIDR    string
	Gateway string
	Family  string
}

// CNIInterface describes one interface returned by CNI result.
type CNIInterface struct {
	Name       string
	MAC        string
	Sandbox    string
	PciID      string
	SocketPath string
	Addresses  []*CNIInterfaceAddress
}

// CNIRoute describes one route returned by CNI result.
type CNIRoute struct {
	Destination string
	Gateway     string
}

// CNIResultInfo contains normalized network data from CNI result.
type CNIResultInfo struct {
	Interfaces []*CNIInterface
	Routes     []*CNIRoute
	DNS        *DNSConfig
}

// PodSandboxNetwork contains PodSandbox-scoped network metadata from CRI.
type PodSandboxNetwork struct {
	SandboxID         string
	SandboxState      string
	PrimaryIP         string
	AdditionalIPs     []string
	HostNetwork       bool
	NamespaceMode     string
	NamespaceTargetID string
	NetNSPath         string
	Hostname          string
	DNS               *DNSConfig
	PortMappings      []*PortMapping
	RuntimeHandler    string
	RuntimeType       string
	StatusSource      string
	ConfigSource      string
	NamespaceSource   string
	CNI               *CNIResultInfo
	Warnings          []string
}

// Client reads CRI container metadata from the runtime service exposed by containerd.
type Client struct {
	socketPath string
}

// NewClient creates a CRI metadata client bound to the runtime socket.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

type containerInfo struct {
	Config *runtimeapi.ContainerConfig `json:"config"`
}

type podSandboxInfo struct {
	Config      *runtimeapi.PodSandboxConfig `json:"config"`
	RuntimeSpec *runtimespec.Spec            `json:"runtimeSpec"`
	Metadata    *podSandboxMetadata          `json:"sandboxMetadata"`
	CNIResult   *cniResultPayload            `json:"cniResult"`
	RuntimeType string                       `json:"runtimeType"`
}

type podSandboxMetadata struct {
	NetNSPath      string
	IP             string
	AdditionalIPs  []string
	RuntimeHandler string
}

type cniResultPayload struct {
	Interfaces map[string]*cniInterfacePayload `json:"Interfaces"`
	DNS        []cniDNSPayload                 `json:"DNS"`
	Routes     []*cniRoutePayload              `json:"Routes"`
}

type cniInterfacePayload struct {
	IPConfigs  []*cniIPConfigPayload `json:"IPConfigs"`
	Mac        string                `json:"Mac"`
	Sandbox    string                `json:"Sandbox"`
	PciID      string                `json:"PciID"`
	SocketPath string                `json:"SocketPath"`
}

type cniIPConfigPayload struct {
	IP      string `json:"IP"`
	Gateway string `json:"Gateway"`
}

type cniRoutePayload struct {
	Dst string `json:"dst"`
	GW  string `json:"gw,omitempty"`
}

type cniDNSPayload struct {
	Nameservers []string `json:"nameservers,omitempty"`
	Domain      string   `json:"domain,omitempty"`
	Search      []string `json:"search,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// InspectContainerMounts fetches CRI config.mounts and status.mounts for a container.
func (c *Client) InspectContainerMounts(ctx context.Context, containerID string) (*ContainerMounts, error) {
	if c == nil || c.socketPath == "" {
		return nil, fmt.Errorf("cri client not configured")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container id is required")
	}

	conn, err := grpc.DialContext(
		ctx,
		c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial cri runtime service: %w", err)
	}
	defer conn.Close()

	resp, err := runtimeapi.NewRuntimeServiceClient(conn).ContainerStatus(ctx, &runtimeapi.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("cri container status: %w", err)
	}

	result := &ContainerMounts{
		StatusMounts: copyProtoMounts(resp.GetStatus().GetMounts()),
	}

	if infoJSON := resp.GetInfo()["info"]; infoJSON != "" {
		var info containerInfo
		if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
			return nil, fmt.Errorf("decode cri info: %w", err)
		}
		if info.Config != nil {
			result.ConfigMounts = copyProtoMounts(info.Config.GetMounts())
		}
	}

	return result, nil
}

// InspectContainerStatus fetches structured CRI container metadata and lifecycle fields.
func (c *Client) InspectContainerStatus(ctx context.Context, containerID string) (*ContainerStatusInfo, error) {
	if c == nil || c.socketPath == "" {
		return nil, fmt.Errorf("cri client not configured")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container id is required")
	}

	conn, err := grpc.DialContext(
		ctx,
		c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial cri runtime service: %w", err)
	}
	defer conn.Close()

	resp, err := runtimeapi.NewRuntimeServiceClient(conn).ContainerStatus(ctx, &runtimeapi.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("cri container status: %w", err)
	}

	return decodeContainerStatus(resp), nil
}

// InspectPodSandboxNetwork fetches structured CRI PodSandbox network metadata and
// best-effort verbose config details for a sandbox.
func (c *Client) InspectPodSandboxNetwork(ctx context.Context, sandboxID string) (*PodSandboxNetwork, error) {
	if c == nil || c.socketPath == "" {
		return nil, fmt.Errorf("cri client not configured")
	}
	if sandboxID == "" {
		return nil, fmt.Errorf("sandbox id is required")
	}

	conn, err := grpc.DialContext(
		ctx,
		c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial cri runtime service: %w", err)
	}
	defer conn.Close()

	resp, err := runtimeapi.NewRuntimeServiceClient(conn).PodSandboxStatus(ctx, &runtimeapi.PodSandboxStatusRequest{
		PodSandboxId: sandboxID,
		Verbose:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("cri pod sandbox status: %w", err)
	}

	return decodePodSandboxNetwork(resp), nil
}

func decodePodSandboxNetwork(resp *runtimeapi.PodSandboxStatusResponse) *PodSandboxNetwork {
	result := &PodSandboxNetwork{}
	if resp == nil {
		result.Warnings = append(result.Warnings, "pod sandbox status response is nil")
		return result
	}

	status := resp.GetStatus()
	if status != nil {
		result.SandboxID = status.GetId()
		result.SandboxState = status.GetState().String()
		result.RuntimeHandler = status.GetRuntimeHandler()
		result.StatusSource = "cri-status"

		if network := status.GetNetwork(); network != nil {
			result.PrimaryIP = network.GetIp()
			for _, ip := range network.GetAdditionalIps() {
				if ip == nil || ip.GetIp() == "" {
					continue
				}
				result.AdditionalIPs = append(result.AdditionalIPs, ip.GetIp())
			}
		}

		if linux := status.GetLinux(); linux != nil && linux.GetNamespaces() != nil {
			applyNamespaceOptions(result, linux.GetNamespaces().GetOptions(), "cri-status")
		}
	}

	infoJSON := resp.GetInfo()["info"]
	if infoJSON == "" {
		return result
	}

	var info podSandboxInfo
	if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("decode cri sandbox info: %v", err))
		return result
	}

	if info.Config != nil {
		result.Hostname = info.Config.GetHostname()
		result.PortMappings = copyProtoPortMappings(info.Config.GetPortMappings())
		if dns := info.Config.GetDnsConfig(); dns != nil {
			result.DNS = &DNSConfig{
				Domain:   "",
				Servers:  append([]string(nil), dns.GetServers()...),
				Searches: append([]string(nil), dns.GetSearches()...),
				Options:  append([]string(nil), dns.GetOptions()...),
			}
		}
		if linux := info.Config.GetLinux(); linux != nil && linux.GetSecurityContext() != nil {
			applyNamespaceOptions(result, linux.GetSecurityContext().GetNamespaceOptions(), "cri-info-config")
		}
		result.ConfigSource = "cri-info"
	}

	if info.Metadata != nil {
		if result.NetNSPath == "" && info.Metadata.NetNSPath != "" {
			result.NetNSPath = info.Metadata.NetNSPath
			result.NamespaceSource = "cri-info-metadata"
		}
		if result.PrimaryIP == "" {
			result.PrimaryIP = info.Metadata.IP
		}
		if len(result.AdditionalIPs) == 0 && len(info.Metadata.AdditionalIPs) > 0 {
			result.AdditionalIPs = append([]string(nil), info.Metadata.AdditionalIPs...)
		}
		if result.RuntimeHandler == "" {
			result.RuntimeHandler = info.Metadata.RuntimeHandler
		}
	}

	if info.RuntimeSpec != nil {
		if path := runtimeSpecNetworkPath(info.RuntimeSpec); path != "" {
			if result.NetNSPath == "" {
				result.NetNSPath = path
				result.NamespaceSource = "cri-info-runtime-spec"
			} else if result.NetNSPath != path {
				result.Warnings = append(result.Warnings, fmt.Sprintf("netns path mismatch: metadata=%s spec=%s", result.NetNSPath, path))
			}
		}
	}

	if info.RuntimeType != "" {
		result.RuntimeType = info.RuntimeType
	}
	if info.CNIResult != nil {
		result.CNI = normalizeCNIResult(info.CNIResult)
	}

	return result
}

func decodeContainerStatus(resp *runtimeapi.ContainerStatusResponse) *ContainerStatusInfo {
	result := &ContainerStatusInfo{}
	if resp == nil {
		return result
	}

	status := resp.GetStatus()
	if status != nil {
		if startedAt := status.GetStartedAt(); startedAt > 0 {
			result.StartedAt = time.Unix(0, startedAt)
		}
		if finishedAt := status.GetFinishedAt(); finishedAt > 0 {
			result.FinishedAt = time.Unix(0, finishedAt)
			exitCode := status.GetExitCode()
			result.ExitCode = &exitCode
		}
		if reason := status.GetReason(); reason != "" {
			result.Reason = reason
		}
		if metadata := status.GetMetadata(); metadata != nil {
			attempt := metadata.GetAttempt()
			result.RestartCount = &attempt
		}
	}

	infoJSON := resp.GetInfo()["info"]
	if infoJSON == "" {
		return result
	}

	var info containerInfo
	if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
		return result
	}

	if info.Config == nil {
		return result
	}

	for _, env := range info.Config.GetEnvs() {
		if env == nil || env.GetKey() == "" {
			continue
		}
		result.Envs = append(result.Envs, ContainerEnv{Key: env.GetKey(), Value: env.GetValue()})
	}

	if linux := info.Config.GetLinux(); linux != nil && linux.GetSecurityContext() != nil {
		if opts := linux.GetSecurityContext().GetNamespaceOptions(); opts != nil {
			mode := opts.GetPid()
			if label := namespaceModeLabel(mode); label != "" {
				result.PIDMode = label
				shared := mode == runtimeapi.NamespaceMode_POD || mode == runtimeapi.NamespaceMode_TARGET || mode == runtimeapi.NamespaceMode_NODE || mode == runtimeapi.NamespaceMode_CONTAINER
				result.SharedPID = &shared
			}
		}
	}

	return result
}

func normalizeCNIResult(result *cniResultPayload) *CNIResultInfo {
	if result == nil {
		return nil
	}

	info := &CNIResultInfo{}
	if len(result.Interfaces) > 0 {
		names := make([]string, 0, len(result.Interfaces))
		for name := range result.Interfaces {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cfg := result.Interfaces[name]
			if cfg == nil {
				continue
			}
			iface := &CNIInterface{
				Name:       name,
				MAC:        cfg.Mac,
				Sandbox:    cfg.Sandbox,
				PciID:      cfg.PciID,
				SocketPath: cfg.SocketPath,
			}
			for _, ipConfig := range cfg.IPConfigs {
				if ipConfig == nil {
					continue
				}
				addr := &CNIInterfaceAddress{}
				if ipConfig.IP != "" {
					addr.CIDR = ipConfig.IP
					if parsedIP := net.ParseIP(ipConfig.IP); parsedIP != nil && parsedIP.To4() != nil {
						addr.Family = "ipv4"
					} else {
						addr.Family = "ipv6"
					}
				}
				if ipConfig.Gateway != "" {
					addr.Gateway = ipConfig.Gateway
				}
				iface.Addresses = append(iface.Addresses, addr)
			}
			info.Interfaces = append(info.Interfaces, iface)
		}
	}

	for _, route := range result.Routes {
		if route == nil {
			continue
		}
		entry := &CNIRoute{}
		entry.Destination = route.Dst
		if route.GW != "" {
			entry.Gateway = route.GW
		}
		info.Routes = append(info.Routes, entry)
	}
	sort.SliceStable(info.Routes, func(i, j int) bool {
		if info.Routes[i].Destination != info.Routes[j].Destination {
			return info.Routes[i].Destination < info.Routes[j].Destination
		}
		return info.Routes[i].Gateway < info.Routes[j].Gateway
	})

	if len(result.DNS) > 0 {
		dns := &DNSConfig{}
		serverSet := map[string]struct{}{}
		searchSet := map[string]struct{}{}
		optionSet := map[string]struct{}{}
		for _, record := range result.DNS {
			if dns.Domain == "" {
				dns.Domain = record.Domain
			}
			for _, server := range record.Nameservers {
				if _, exists := serverSet[server]; exists {
					continue
				}
				serverSet[server] = struct{}{}
				dns.Servers = append(dns.Servers, server)
			}
			for _, search := range record.Search {
				if _, exists := searchSet[search]; exists {
					continue
				}
				searchSet[search] = struct{}{}
				dns.Searches = append(dns.Searches, search)
			}
			for _, option := range record.Options {
				if _, exists := optionSet[option]; exists {
					continue
				}
				optionSet[option] = struct{}{}
				dns.Options = append(dns.Options, option)
			}
		}
		info.DNS = dns
	}

	if len(info.Interfaces) == 0 && len(info.Routes) == 0 && info.DNS == nil {
		return nil
	}
	return info
}

func applyNamespaceOptions(result *PodSandboxNetwork, options *runtimeapi.NamespaceOption, source string) {
	if result == nil || options == nil {
		return
	}

	mode := options.GetNetwork()
	if modeLabel := namespaceModeLabel(mode); modeLabel != "" {
		result.NamespaceMode = modeLabel
		result.NamespaceSource = source
		switch mode {
		case runtimeapi.NamespaceMode_NODE:
			result.HostNetwork = true
		case runtimeapi.NamespaceMode_POD:
			result.HostNetwork = false
		}
	}
	if targetID := options.GetTargetId(); targetID != "" {
		result.NamespaceTargetID = targetID
	}
}

func namespaceModeLabel(mode runtimeapi.NamespaceMode) string {
	switch mode {
	case runtimeapi.NamespaceMode_POD,
		runtimeapi.NamespaceMode_CONTAINER,
		runtimeapi.NamespaceMode_NODE,
		runtimeapi.NamespaceMode_TARGET:
		return mode.String()
	default:
		return ""
	}
}

func runtimeSpecNetworkPath(spec *runtimespec.Spec) string {
	if spec == nil || spec.Linux == nil {
		return ""
	}
	for _, ns := range spec.Linux.Namespaces {
		if string(ns.Type) != "network" {
			continue
		}
		return ns.Path
	}
	return ""
}

func copyProtoPortMappings(protoMappings []*runtimeapi.PortMapping) []*PortMapping {
	if len(protoMappings) == 0 {
		return nil
	}

	mappings := make([]*PortMapping, 0, len(protoMappings))
	for _, mapping := range protoMappings {
		if mapping == nil {
			continue
		}
		mappings = append(mappings, &PortMapping{
			HostIP:        mapping.GetHostIp(),
			HostPort:      uint16(mapping.GetHostPort()),
			ContainerPort: uint16(mapping.GetContainerPort()),
			Protocol:      strings.ToLower(mapping.GetProtocol().String()),
		})
	}

	return mappings
}

// MountOptions converts CRI mount flags into an OCI-like option slice for display.
func MountOptions(mount *Mount) []string {
	if mount == nil {
		return nil
	}

	options := make([]string, 0, 5)
	if mount.Readonly {
		options = append(options, "ro")
	} else {
		options = append(options, "rw")
	}
	if mount.RecursiveReadOnly {
		options = append(options, "rro")
	}
	if mount.SelinuxRelabel {
		options = append(options, "z")
	}

	switch mount.Propagation {
	case runtimeapi.MountPropagation_PROPAGATION_PRIVATE:
		options = append(options, "rprivate")
	case runtimeapi.MountPropagation_PROPAGATION_HOST_TO_CONTAINER:
		options = append(options, "rslave")
	case runtimeapi.MountPropagation_PROPAGATION_BIDIRECTIONAL:
		options = append(options, "rshared")
	}

	if mount.Image != "" {
		options = append(options, "image="+mount.Image)
	}

	return options
}

func copyProtoMounts(protoMounts []*runtimeapi.Mount) []*Mount {
	if len(protoMounts) == 0 {
		return nil
	}

	mounts := make([]*Mount, 0, len(protoMounts))
	for _, protoMount := range protoMounts {
		if protoMount == nil {
			continue
		}
		mounts = append(mounts, &Mount{
			ContainerPath:     protoMount.GetContainerPath(),
			HostPath:          protoMount.GetHostPath(),
			Readonly:          protoMount.GetReadonly(),
			SelinuxRelabel:    protoMount.GetSelinuxRelabel(),
			RecursiveReadOnly: protoMount.GetRecursiveReadOnly(),
			Propagation:       protoMount.GetPropagation(),
			Image:             protoImage(protoMount.GetImage()),
		})
	}

	return mounts
}

func protoImage(image *runtimeapi.ImageSpec) string {
	if image == nil {
		return ""
	}
	return image.GetImage()
}

func unixDialer(ctx context.Context, addr string) (net.Conn, error) {
	addr = strings.TrimPrefix(addr, "unix://")
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", addr)
}
