package cri

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
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
	Status       string
	StartedAt    time.Time
	FinishedAt   time.Time
	ExitCode     *int32
	Reason       string
	RestartCount *uint32
	PID          uint32
	PIDMode      string
	SharedPID    *bool
	Envs         []ContainerEnv

	// Fields from ContainerStatus (authoritative; ListContainers response may omit them).
	Annotations map[string]string
	Labels      map[string]string
	Image       string
	ImageRef    string
	Name        string

	// Stdio-related fields extracted from CRI config and status.
	TTY           *bool
	Stdin         *bool
	StdinOnce     *bool
	ConfigLogPath string // ContainerConfig.log_path (may be relative)
	StatusLogPath string // ContainerStatus.log_path (usually absolute)

	// Config-level image fields from CRI verbose info (info.config.image).
	ConfigImageID  string // info.config.image.image
	ConfigImageRef string // info.config.image.user_specified_image
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
	DNS               *runtime.DNSConfig
	PortMappings      []*runtime.PortMapping
	StatusSource      string
	ConfigSource      string
	NamespaceSource   string
	CNI               *runtime.CNIResultInfo
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
	PID         int                         `json:"pid"`
	Config      *runtimeapi.ContainerConfig `json:"config"`
	RuntimeSpec *runtimespec.Spec           `json:"runtimeSpec"`
}

type podSandboxInfo struct {
	Config      *runtimeapi.PodSandboxConfig `json:"config"`
	RuntimeSpec *runtimespec.Spec            `json:"runtimeSpec"`
	Metadata    *podSandboxMetadataWrapper   `json:"sandboxMetadata"`
	CNIResult   *cniResultPayload            `json:"cniResult"`
	RuntimeType string                       `json:"runtimeType"`
}

type podSandboxMetadataWrapper struct {
	Metadata *podSandboxMetadataInner `json:"Metadata"`
	Version  string                   `json:"Version"`
}

type podSandboxMetadataInner struct {
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

// CRI-O returns CNI results in standard CNI spec format (lowercase, arrays).
type crioCNIResultPayload struct {
	Interfaces []crioCNIInterface `json:"interfaces"`
	IPs        []crioCNIIP        `json:"ips"`
	Routes     []cniRoutePayload  `json:"routes"`
	DNS        cniDNSPayload      `json:"dns"`
}

type crioCNIInterface struct {
	Name    string `json:"name"`
	Mac     string `json:"mac"`
	Sandbox string `json:"sandbox"`
}

type crioCNIIP struct {
	Address   string `json:"address"`
	Gateway   string `json:"gateway"`
	Interface *int   `json:"interface"`
}

// InspectContainerMounts fetches CRI config.mounts and status.mounts for a container.
func (c *Client) InspectContainerMounts(ctx context.Context, containerID string) (*ContainerMounts, error) {
	if c == nil || c.socketPath == "" {
		return nil, fmt.Errorf("cri client not configured")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container id is required")
	}

	conn, err := grpc.NewClient(
		"unix://"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
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

	conn, err := grpc.NewClient(
		"unix://"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
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

	conn, err := grpc.NewClient(
		"unix://"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(unixDialer),
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

// decodePodSandboxNetwork is a unified decoder that handles both CRI-O and
// containerd info formats:
//   - containerd populates config, sandboxMetadata, cniResult, runtimeType
//   - CRI-O only populates runtimeSpec (no config/sandboxMetadata/cniResult)
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

	// containerd populates config with PodSandboxConfig.
	if info.Config != nil {
		result.Hostname = info.Config.GetHostname()
		result.PortMappings = copyProtoPortMappings(info.Config.GetPortMappings())
		if dns := info.Config.GetDnsConfig(); dns != nil {
			result.DNS = &runtime.DNSConfig{
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

	// containerd populates sandboxMetadata with nested Metadata.
	if info.Metadata != nil && info.Metadata.Metadata != nil {
		meta := info.Metadata.Metadata
		if result.NetNSPath == "" && meta.NetNSPath != "" {
			result.NetNSPath = meta.NetNSPath
			result.NamespaceSource = "cri-info-metadata"
		}
		if result.PrimaryIP == "" {
			result.PrimaryIP = meta.IP
		}
		if len(result.AdditionalIPs) == 0 && len(meta.AdditionalIPs) > 0 {
			result.AdditionalIPs = append([]string(nil), meta.AdditionalIPs...)
		}
	}

	// Both CRI-O and containerd populate runtimeSpec.
	if info.RuntimeSpec != nil {
		// CRI-O has no config; fall back to runtimeSpec for hostname.
		if result.Hostname == "" {
			result.Hostname = info.RuntimeSpec.Hostname
		}

		if path := runtimeSpecNetworkPath(info.RuntimeSpec); path != "" {
			if result.NetNSPath == "" {
				result.NetNSPath = path
				result.NamespaceSource = "cri-info-runtime-spec"
			} else if result.NetNSPath != path {
				result.Warnings = append(result.Warnings, fmt.Sprintf("netns path mismatch: metadata=%s spec=%s", result.NetNSPath, path))
			}
		}

		// CRI-O exposes CNI result via annotation in standard CNI spec format.
		if cniJSON, ok := info.RuntimeSpec.Annotations["io.kubernetes.cri-o.CNIResult"]; ok && cniJSON != "" {
			var cniPayload crioCNIResultPayload
			if err := json.Unmarshal([]byte(cniJSON), &cniPayload); err == nil {
				if parsed := normalizeCrioCNIResult(&cniPayload); parsed != nil {
					result.CNI = parsed
				}
			}
		}
	}

	// containerd populates top-level cniResult.
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

		switch status.GetState() {
		case runtimeapi.ContainerState_CONTAINER_CREATED:
			result.Status = "created"
		case runtimeapi.ContainerState_CONTAINER_RUNNING:
			result.Status = "running"
		case runtimeapi.ContainerState_CONTAINER_EXITED:
			result.Status = "stopped"
		default:
			result.Status = "unknown"
		}
		result.StatusLogPath = status.GetLogPath()
		if ann := status.GetAnnotations(); len(ann) > 0 {
			result.Annotations = ann
		}
		if labels := status.GetLabels(); len(labels) > 0 {
			result.Labels = labels
		}
		if img := status.GetImage(); img != nil && img.GetImage() != "" {
			result.Image = img.GetImage()
		}
		if imgRef := status.GetImageRef(); imgRef != "" {
			result.ImageRef = imgRef
		}
		if metadata := status.GetMetadata(); metadata != nil {
			attempt := metadata.GetAttempt()
			result.RestartCount = &attempt
			if name := metadata.GetName(); name != "" {
				result.Name = name
			}
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

	if info.PID > 0 {
		result.PID = uint32(info.PID)
	}

	// CRI-O populates runtimeSpec with the OCI spec, whose Annotations
	// carry authoritative io.kubernetes.cri-o.* keys.
	if info.RuntimeSpec != nil && len(info.RuntimeSpec.Annotations) > 0 {
		if result.Annotations == nil {
			result.Annotations = make(map[string]string, len(info.RuntimeSpec.Annotations))
		}
		for k, v := range info.RuntimeSpec.Annotations {
			result.Annotations[k] = v
		}
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

	// Config-level image fields (info.config.image).
	if img := info.Config.GetImage(); img != nil {
		result.ConfigImageID = img.GetImage()
		result.ConfigImageRef = img.GetUserSpecifiedImage()
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

	// Stdio-related fields from CRI config.
	tty := info.Config.GetTty()
	result.TTY = &tty
	stdin := info.Config.GetStdin()
	result.Stdin = &stdin
	stdinOnce := info.Config.GetStdinOnce()
	result.StdinOnce = &stdinOnce
	result.ConfigLogPath = info.Config.GetLogPath()

	return result
}

func normalizeCNIResult(result *cniResultPayload) *runtime.CNIResultInfo {
	if result == nil {
		return nil
	}

	info := &runtime.CNIResultInfo{}
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
			iface := &runtime.CNIInterface{
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
				addr := &runtime.CNIInterfaceAddress{}
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
		entry := &runtime.CNIRoute{}
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
		dns := &runtime.DNSConfig{}
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

// normalizeCrioCNIResult converts CRI-O's standard CNI spec format to runtime.CNIResultInfo.
// CRI-O returns interfaces as an array and IPs reference interfaces by index.
func normalizeCrioCNIResult(result *crioCNIResultPayload) *runtime.CNIResultInfo {
	if result == nil {
		return nil
	}

	info := &runtime.CNIResultInfo{}

	// Build interface list and map IPs to interfaces by index.
	ifaces := make([]*runtime.CNIInterface, len(result.Interfaces))
	for i, ifc := range result.Interfaces {
		ifaces[i] = &runtime.CNIInterface{
			Name:    ifc.Name,
			MAC:     ifc.Mac,
			Sandbox: ifc.Sandbox,
		}
	}

	for _, ip := range result.IPs {
		addr := &runtime.CNIInterfaceAddress{
			Gateway: ip.Gateway,
		}
		if ip.Address != "" {
			addr.CIDR = ip.Address
			// Parse IP from CIDR to determine family.
			ipStr := ip.Address
			if idx := strings.IndexByte(ipStr, '/'); idx >= 0 {
				ipStr = ipStr[:idx]
			}
			if parsedIP := net.ParseIP(ipStr); parsedIP != nil && parsedIP.To4() != nil {
				addr.Family = "ipv4"
			} else {
				addr.Family = "ipv6"
			}
		}

		if ip.Interface != nil && *ip.Interface >= 0 && *ip.Interface < len(ifaces) {
			ifaces[*ip.Interface].Addresses = append(ifaces[*ip.Interface].Addresses, addr)
		}
	}

	// Sort interfaces by name for stable output.
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Name < ifaces[j].Name })
	info.Interfaces = ifaces

	for _, route := range result.Routes {
		entry := &runtime.CNIRoute{Destination: route.Dst}
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

	dns := result.DNS
	if len(dns.Nameservers) > 0 || len(dns.Search) > 0 || len(dns.Options) > 0 || dns.Domain != "" {
		info.DNS = &runtime.DNSConfig{
			Domain:   dns.Domain,
			Servers:  append([]string(nil), dns.Nameservers...),
			Searches: append([]string(nil), dns.Search...),
			Options:  append([]string(nil), dns.Options...),
		}
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

func copyProtoPortMappings(protoMappings []*runtimeapi.PortMapping) []*runtime.PortMapping {
	if len(protoMappings) == 0 {
		return nil
	}

	mappings := make([]*runtime.PortMapping, 0, len(protoMappings))
	for _, mapping := range protoMappings {
		if mapping == nil {
			continue
		}
		mappings = append(mappings, &runtime.PortMapping{
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
