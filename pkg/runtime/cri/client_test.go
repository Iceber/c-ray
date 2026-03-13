package cri

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestMountOptions(t *testing.T) {
	mount := &Mount{
		Readonly:          true,
		RecursiveReadOnly: true,
		SelinuxRelabel:    true,
		Propagation:       runtimeapi.MountPropagation_PROPAGATION_BIDIRECTIONAL,
		Image:             "registry.example/app:latest",
	}

	got := MountOptions(mount)
	want := []string{"ro", "rro", "z", "rshared", "image=registry.example/app:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MountOptions() = %v, want %v", got, want)
	}
}

func TestDecodePodSandboxNetwork(t *testing.T) {
	infoJSON, err := json.Marshal(podSandboxInfo{
		Config: &runtimeapi.PodSandboxConfig{
			Hostname: "pod-a",
			DnsConfig: &runtimeapi.DNSConfig{
				Servers:  []string{"10.96.0.10"},
				Searches: []string{"svc.cluster.local"},
				Options:  []string{"ndots:5"},
			},
			PortMappings: []*runtimeapi.PortMapping{{
				Protocol:      runtimeapi.Protocol_TCP,
				ContainerPort: 8080,
				HostPort:      30080,
				HostIp:        "0.0.0.0",
			}},
			Linux: &runtimeapi.LinuxPodSandboxConfig{
				SecurityContext: &runtimeapi.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtimeapi.NamespaceOption{Network: runtimeapi.NamespaceMode_POD},
				},
			},
		},
		Metadata: &podSandboxMetadata{
			NetNSPath: "/var/run/netns/test",
		},
		CNIResult: &cniResultPayload{
			Interfaces: map[string]*cniInterfacePayload{
				"eth0": {
					Mac:     "02:42:ac:11:00:02",
					Sandbox: "/var/run/netns/test",
					IPConfigs: []*cniIPConfigPayload{{
						IP:      "10.244.0.12",
						Gateway: "10.244.0.1",
					}},
				},
			},
			Routes: []*cniRoutePayload{{Dst: "0.0.0.0/0", GW: "10.244.0.1"}},
			DNS:    []cniDNSPayload{{Nameservers: []string{"10.96.0.10"}, Search: []string{"svc.cluster.local"}, Options: []string{"ndots:5"}, Domain: "cluster.local"}},
		},
		RuntimeType: "io.containerd.runc.v2",
		RuntimeSpec: &runtimespec.Spec{
			Linux: &runtimespec.Linux{Namespaces: []runtimespec.LinuxNamespace{{Type: runtimespec.NetworkNamespace, Path: "/var/run/netns/test"}}},
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	resp := &runtimeapi.PodSandboxStatusResponse{
		Status: &runtimeapi.PodSandboxStatus{
			Id:             "sandbox-1",
			State:          runtimeapi.PodSandboxState_SANDBOX_READY,
			RuntimeHandler: "runc",
			Network: &runtimeapi.PodSandboxNetworkStatus{
				Ip:            "10.244.0.12",
				AdditionalIps: []*runtimeapi.PodIP{{Ip: "fd00::12"}},
			},
			Linux: &runtimeapi.LinuxPodSandboxStatus{
				Namespaces: &runtimeapi.Namespace{Options: &runtimeapi.NamespaceOption{Network: runtimeapi.NamespaceMode_POD}},
			},
		},
		Info: map[string]string{"info": string(infoJSON)},
	}

	got := decodePodSandboxNetwork(resp)
	if got.SandboxID != "sandbox-1" {
		t.Fatalf("SandboxID = %s", got.SandboxID)
	}
	if got.PrimaryIP != "10.244.0.12" {
		t.Fatalf("PrimaryIP = %s", got.PrimaryIP)
	}
	if !reflect.DeepEqual(got.AdditionalIPs, []string{"fd00::12"}) {
		t.Fatalf("AdditionalIPs = %v", got.AdditionalIPs)
	}
	if got.NamespaceMode != runtimeapi.NamespaceMode_POD.String() {
		t.Fatalf("NamespaceMode = %s", got.NamespaceMode)
	}
	if got.HostNetwork {
		t.Fatal("HostNetwork = true, want false")
	}
	if got.Hostname != "pod-a" {
		t.Fatalf("Hostname = %s", got.Hostname)
	}
	if got.NetNSPath != "/var/run/netns/test" {
		t.Fatalf("NetNSPath = %s", got.NetNSPath)
	}
	if got.RuntimeType != "io.containerd.runc.v2" {
		t.Fatalf("RuntimeType = %s", got.RuntimeType)
	}
	if got.DNS == nil || !reflect.DeepEqual(got.DNS.Servers, []string{"10.96.0.10"}) {
		t.Fatalf("DNS = %+v", got.DNS)
	}
	if len(got.PortMappings) != 1 {
		t.Fatalf("PortMappings len = %d", len(got.PortMappings))
	}
	if got.PortMappings[0].Protocol != "tcp" {
		t.Fatalf("Protocol = %s", got.PortMappings[0].Protocol)
	}
	if got.CNI == nil || len(got.CNI.Interfaces) != 1 || len(got.CNI.Routes) != 1 {
		t.Fatalf("CNI = %+v", got.CNI)
	}
	if got.CNI.Interfaces[0].Addresses[0].CIDR != "10.244.0.12" {
		t.Fatalf("CNI address = %+v", got.CNI.Interfaces[0].Addresses[0])
	}
	if got.CNI.DNS == nil || got.CNI.DNS.Domain != "cluster.local" {
		t.Fatalf("CNI DNS = %+v", got.CNI.DNS)
	}
}

func TestDecodePodSandboxNetworkWarnsOnMalformedInfo(t *testing.T) {
	got := decodePodSandboxNetwork(&runtimeapi.PodSandboxStatusResponse{
		Status: &runtimeapi.PodSandboxStatus{Id: "sandbox-1"},
		Info:   map[string]string{"info": "{"},
	})
	if len(got.Warnings) == 0 {
		t.Fatal("Warnings = 0, want malformed info warning")
	}
}

func TestDecodeContainerStatus(t *testing.T) {
	infoJSON, err := json.Marshal(containerInfo{
		Config: &runtimeapi.ContainerConfig{
			Envs: []*runtimeapi.KeyValue{{Key: "KUBERNETES_SERVICE_HOST", Value: "10.96.0.1"}, {Key: "HOME", Value: "/root"}},
			Linux: &runtimeapi.LinuxContainerConfig{
				SecurityContext: &runtimeapi.LinuxContainerSecurityContext{
					NamespaceOptions: &runtimeapi.NamespaceOption{Pid: runtimeapi.NamespaceMode_POD},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	resp := &runtimeapi.ContainerStatusResponse{
		Status: &runtimeapi.ContainerStatus{
			Metadata:   &runtimeapi.ContainerMetadata{Attempt: 3},
			StartedAt:  time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC).UnixNano(),
			FinishedAt: time.Date(2026, 3, 13, 10, 5, 0, 0, time.UTC).UnixNano(),
			ExitCode:   137,
			Reason:     "OOMKilled",
		},
		Info: map[string]string{"info": string(infoJSON)},
	}

	got := decodeContainerStatus(resp)
	if got == nil {
		t.Fatal("decodeContainerStatus() = nil")
	}
	if got.RestartCount == nil || *got.RestartCount != 3 {
		t.Fatalf("RestartCount = %v", got.RestartCount)
	}
	if got.ExitCode == nil || *got.ExitCode != 137 {
		t.Fatalf("ExitCode = %v", got.ExitCode)
	}
	if got.Reason != "OOMKilled" {
		t.Fatalf("Reason = %s", got.Reason)
	}
	if got.StartedAt.IsZero() || got.FinishedAt.IsZero() {
		t.Fatalf("times not populated: started=%v finished=%v", got.StartedAt, got.FinishedAt)
	}
	if got.SharedPID == nil || !*got.SharedPID {
		t.Fatalf("SharedPID = %v", got.SharedPID)
	}
	if got.PIDMode != runtimeapi.NamespaceMode_POD.String() {
		t.Fatalf("PIDMode = %s", got.PIDMode)
	}
	if len(got.Envs) != 2 {
		t.Fatalf("Envs len = %d", len(got.Envs))
	}
	if got.Envs[0].Key != "KUBERNETES_SERVICE_HOST" {
		t.Fatalf("first env = %+v", got.Envs[0])
	}
}
