package runtime

import "testing"

func TestNormalizeCGroupPathSystemd(t *testing.T) {
	raw := "kubelet-kubepods-besteffort-pod26a1a59b_8013_459b_9a20_a611facb1888.slice:cri-containerd:1207db63eaeb133a85d2b614d12d95d4dff3f29af60d7a7b3c7d39abdf4bd119"
	want := "/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice/kubelet-kubepods-besteffort-pod26a1a59b_8013_459b_9a20_a611facb1888.slice/cri-containerd-1207db63eaeb133a85d2b614d12d95d4dff3f29af60d7a7b3c7d39abdf4bd119.scope"

	if got := NormalizeCGroupPath(raw); got != want {
		t.Fatalf("NormalizeCGroupPath() = %q, want %q", got, want)
	}
}

func TestNormalizeCGroupPathCgroupfs(t *testing.T) {
	raw := "kubepods/burstable/pod1234/abcdef"
	want := "/kubepods/burstable/pod1234/abcdef"

	if got := NormalizeCGroupPath(raw); got != want {
		t.Fatalf("NormalizeCGroupPath() = %q, want %q", got, want)
	}
}
