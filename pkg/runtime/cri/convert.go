package cri

import (
	"fmt"

	"github.com/icebergu/c-ray/pkg/runtime"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
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

	if len(src.PortMappings) > 0 {
		dst.PortMappings = src.PortMappings
	}

	if src.DNS != nil {
		dst.DNS = src.DNS
	}

	if src.CNI != nil {
		dst.CNI = src.CNI
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

// BuildEnvironment constructs the environment variable list from OCI spec or CRI status.
// OCI spec takes precedence when the process environment is available.
func BuildEnvironment(spec *runtimespec.Spec, criStatus *ContainerStatus) []runtime.EnvVar {
	if spec != nil && spec.Process != nil && len(spec.Process.Env) > 0 {
		return runtime.ParseEnvVars(spec.Process.Env)
	}
	if criStatus != nil && len(criStatus.Envs) > 0 {
		envs := make([]runtime.EnvVar, 0, len(criStatus.Envs))
		for _, e := range criStatus.Envs {
			envs = append(envs, runtime.EnvVar{
				Key:          e.Key,
				Value:        e.Value,
				IsKubernetes: runtime.IsKubernetesEnvKey(e.Key),
			})
		}
		return envs
	}
	return nil
}
