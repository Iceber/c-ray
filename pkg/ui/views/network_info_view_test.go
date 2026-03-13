package views

import (
	"strings"
	"testing"

	"github.com/icebergu/c-ray/pkg/models"
)

func TestBuildNetworkInterfacesNodeFallsBackToObservedInterfaces(t *testing.T) {
	network := &models.PodNetworkInfo{
		ObservedInterfaces: []*models.NetworkStats{{
			Interface: "eth0",
			RxBytes:   2048,
			RxPackets: 20,
			TxBytes:   4096,
			TxPackets: 40,
			RxErrors:  1,
			TxErrors:  3,
		}},
	}

	node := buildNetworkInterfacesNode(network)
	text := joinNodeTexts(node)
	if !strings.Contains(text, "Interfaces (1)") {
		t.Fatalf("interfaces node missing count: %s", text)
	}
	if !strings.Contains(text, "Source: [white]procfs") && !strings.Contains(text, "Source: procfs") {
		t.Fatalf("interfaces node missing procfs source: %s", text)
	}
	if !strings.Contains(text, "eth0") {
		t.Fatalf("interfaces node missing observed interface name: %s", text)
	}
	if !strings.Contains(text, "RX: [white]2048 bytes / 20 packets") && !strings.Contains(text, "RX: 2048 bytes / 20 packets") {
		t.Fatalf("interfaces node missing rx counters: %s", text)
	}
}
