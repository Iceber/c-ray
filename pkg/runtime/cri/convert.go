package cri

import (
	"fmt"

	"github.com/icebergu/c-ray/pkg/runtime"
)

// ApplyCRINetwork maps CRI sandbox network metadata onto a v1 PodNetworkInfo,
// merging warnings and detecting netns path mismatches.
func ApplyCRINetwork(dst *runtime.PodNetworkInfo, src *PodSandboxNetwork) {
	dst.SandboxState = src.SandboxState
	dst.PrimaryIP = src.PrimaryIP
	dst.AdditionalIPs = append([]string(nil), src.AdditionalIPs...)
	dst.HostNetwork = src.HostNetwork
	dst.NamespaceMode = src.NamespaceMode
	dst.Hostname = src.Hostname
	dst.RuntimeHandler = src.RuntimeHandler
	dst.RuntimeType = src.RuntimeType

	if len(src.PortMappings) > 0 {
		dst.PortMappings = ConvertPortMappings(src.PortMappings)
	}

	if src.DNS != nil {
		dst.DNS = &runtime.DNSConfig{
			Domain:   src.DNS.Domain,
			Servers:  append([]string(nil), src.DNS.Servers...),
			Searches: append([]string(nil), src.DNS.Searches...),
			Options:  append([]string(nil), src.DNS.Options...),
		}
	}

	if src.CNI != nil {
		dst.CNI = ConvertCNIResult(src.CNI)
	}

	if src.NetNSPath != "" {
		if dst.NetNSPath != "" && dst.NetNSPath != src.NetNSPath {
			dst.Warnings = append(dst.Warnings,
				fmt.Sprintf("netns path mismatch: spec=%s cri=%s", dst.NetNSPath, src.NetNSPath))
		}
		dst.NetNSPath = src.NetNSPath
	}

	dst.Warnings = append(dst.Warnings, src.Warnings...)
}

// ConvertPortMappings converts CRI port mappings to v1 port mappings.
func ConvertPortMappings(src []*PortMapping) []*runtime.PortMapping {
	out := make([]*runtime.PortMapping, 0, len(src))
	for _, pm := range src {
		if pm == nil {
			continue
		}
		out = append(out, &runtime.PortMapping{
			HostIP:        pm.HostIP,
			HostPort:      pm.HostPort,
			ContainerPort: pm.ContainerPort,
			Protocol:      pm.Protocol,
		})
	}
	return out
}

// ConvertCNIResult converts a CRI CNI result tree to v1 types.
func ConvertCNIResult(src *CNIResultInfo) *runtime.CNIResultInfo {
	if src == nil {
		return nil
	}
	dst := &runtime.CNIResultInfo{}

	for _, iface := range src.Interfaces {
		if iface == nil {
			continue
		}
		entry := &runtime.CNIInterface{
			Name:       iface.Name,
			MAC:        iface.MAC,
			Sandbox:    iface.Sandbox,
			PciID:      iface.PciID,
			SocketPath: iface.SocketPath,
		}
		for _, addr := range iface.Addresses {
			if addr == nil {
				continue
			}
			entry.Addresses = append(entry.Addresses, &runtime.CNIInterfaceAddress{
				CIDR:    addr.CIDR,
				Gateway: addr.Gateway,
				Family:  addr.Family,
			})
		}
		dst.Interfaces = append(dst.Interfaces, entry)
	}

	for _, route := range src.Routes {
		if route == nil {
			continue
		}
		dst.Routes = append(dst.Routes, &runtime.CNIRoute{
			Destination: route.Destination,
			Gateway:     route.Gateway,
		})
	}

	if src.DNS != nil {
		dst.DNS = &runtime.DNSConfig{
			Domain:   src.DNS.Domain,
			Servers:  append([]string(nil), src.DNS.Servers...),
			Searches: append([]string(nil), src.DNS.Searches...),
			Options:  append([]string(nil), src.DNS.Options...),
		}
	}

	return dst
}

// ShouldAttachPodNetwork reports whether a PodNetworkInfo carries enough
// data to be worth attaching to a container detail view.
func ShouldAttachPodNetwork(info *runtime.PodNetworkInfo) bool {
	return info.SandboxID != "" ||
		info.PrimaryIP != "" ||
		len(info.AdditionalIPs) > 0 ||
		info.NetNSPath != "" ||
		len(info.PortMappings) > 0 ||
		info.Hostname != "" ||
		len(info.ObservedInterfaces) > 0 ||
		len(info.Warnings) > 0
}
