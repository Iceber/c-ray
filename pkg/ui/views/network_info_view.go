package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// NetworkInfoView renders the Network page.
type NetworkInfoView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView
	container runtime.Container
	mu        sync.Mutex
}

// NewNetworkInfoView creates a new network info view.
func NewNetworkInfoView(app *tview.Application) *NetworkInfoView {
	v := &NetworkInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	components.InitTreeView(v.tree)
	v.tree.SetRoot(tview.NewTreeNode(components.Muted("No network data")).SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *NetworkInfoView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
	v.renderEmpty()
	v.updateStatusBar()
}

// Refresh loads network metadata from the container handle.
func (v *NetworkInfoView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return nil
	}

	netState, err := c.Network(ctx)
	if err != nil {
		v.renderError(err)
		return err
	}

	v.render(netState)
	v.updateStatusBar()
	return nil
}

func (v *NetworkInfoView) renderError(err error) {
	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode(components.Accent("Network")).SetSelectable(false).SetExpanded(true)
		root.AddChild(tview.NewTreeNode(fmt.Sprintf("[%s]Failed to load network: %v[-]", components.ColorName(components.ColorFgError), err)).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

// HandleInput processes tree interaction.
func (v *NetworkInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	return components.HandleTreeInput(event, v.tree, v.expandAll, nil)
}

// GetFocusPrimitive returns the tree focus target.
func (v *NetworkInfoView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func (v *NetworkInfoView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode(components.Accent("Network")).SetSelectable(false).SetExpanded(true)
		root.AddChild(tview.NewTreeNode(components.Muted("Refresh to resolve sandbox, DNS, interfaces and routes")).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

func (v *NetworkInfoView) render(netState *runtime.ContainerNetworkState) {
	root := tview.NewTreeNode(components.Accent("Network")).SetSelectable(false).SetExpanded(true)
	if netState == nil || netState.PodNetwork == nil {
		root.AddChild(tview.NewTreeNode(components.Muted("No network metadata available")).SetSelectable(false))
		queueUpdateDraw(v.app, func() {
			v.tree.SetRoot(root)
			v.tree.SetCurrentNode(root)
		})
		return
	}

	network := netState.PodNetwork
	root.AddChild(buildSandboxNodeV1(network))
	root.AddChild(buildDNSNodeV1("Sandbox DNS", network.DNS, true))
	root.AddChild(buildInterfacesNodeV1(network))
	root.AddChild(buildRoutesNodeV1(network.CNI))
	root.AddChild(buildDNSNodeV1("CNI DNS", cniDNSV1(network.CNI), false))

	firstNode := selectFirstNetworkNode(root)
	queueUpdateDraw(v.app, func() {
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(firstNode)
	})
}

func (v *NetworkInfoView) expandAll() {
	root := v.tree.GetRoot()
	components.ExpandAllNodes(root)
	v.tree.SetCurrentNode(root)
}

func (v *NetworkInfoView) updateStatusBar() {
	v.statusBar.SetText(fmt.Sprintf(
		" %s  |  %s  %s",
		components.Muted("Network: sandbox, CNI metadata and observed traffic"),
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
	))
}

// --- Builder helpers ---

func buildSandboxNodeV1(network *runtime.PodNetworkInfo) *tview.TreeNode {
	node := tview.NewTreeNode(fmt.Sprintf("[%s::b]Sandbox[-:-:-]", components.ColorName(components.ColorFgAccentAlt))).SetSelectable(true).SetExpanded(true)
	rows := []string{
		"Sandbox ID: " + fallbackNetField(network.SandboxID),
		"State: " + fallbackNetField(network.SandboxState),
		"Primary IP: " + fallbackNetField(network.PrimaryIP),
		"Additional IPs: " + strings.Join(nonEmptyOrDashV1(network.AdditionalIPs), ", "),
		fmt.Sprintf("Host Network: %t", network.HostNetwork),
		"Namespace Mode: " + fallbackNetField(network.NamespaceMode),
		"NetNS Path: " + fallbackNetField(network.NetNSPath),
		"Hostname: " + fallbackNetField(network.Hostname),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted(row))).SetSelectable(true))
	}
	if len(network.PortMappings) > 0 {
		portsNode := tview.NewTreeNode(fmt.Sprintf("%s", components.Accent(fmt.Sprintf("  Port Mappings (%d)", len(network.PortMappings))))).
			SetSelectable(true).SetExpanded(false)
		for _, port := range network.PortMappings {
			portsNode.AddChild(tview.NewTreeNode(fmt.Sprintf("    %s",
				components.Muted(fmt.Sprintf("%s:%d -> %d/%s", fallbackNetField(port.HostIP), port.HostPort, port.ContainerPort, strings.ToLower(port.Protocol))))).SetSelectable(true))
		}
		node.AddChild(portsNode)
	}
	if len(network.Warnings) > 0 {
		warningsNode := tview.NewTreeNode(fmt.Sprintf("[%s::b]  Warnings (%d)[-:-:-]", components.ColorName(components.ColorFgWarn), len(network.Warnings))).
			SetSelectable(true).SetExpanded(false)
		for _, warning := range network.Warnings {
			warningsNode.AddChild(tview.NewTreeNode(fmt.Sprintf("    %s", components.Muted(warning))).SetSelectable(true))
		}
		node.AddChild(warningsNode)
	}
	return node
}

func buildDNSNodeV1(title string, dns *runtime.DNSConfig, expanded bool) *tview.TreeNode {
	node := tview.NewTreeNode(components.Accent(title)).SetSelectable(true).SetExpanded(expanded)
	if dns == nil {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted("No DNS data"))).SetSelectable(false))
		return node
	}
	rows := []string{
		"Domain: " + fallbackNetField(dns.Domain),
		"Servers: " + strings.Join(nonEmptyOrDashV1(dns.Servers), ", "),
		"Searches: " + strings.Join(nonEmptyOrDashV1(dns.Searches), ", "),
		"Options: " + strings.Join(nonEmptyOrDashV1(dns.Options), ", "),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted(row))).SetSelectable(true))
	}
	return node
}

func buildInterfacesNodeV1(network *runtime.PodNetworkInfo) *tview.TreeNode {
	count := len(network.ObservedInterfaces)
	if network.CNI != nil && len(network.CNI.Interfaces) > count {
		count = len(network.CNI.Interfaces)
	}

	node := tview.NewTreeNode(fmt.Sprintf("%s %s",
		components.Accent("Interfaces"), components.Muted(fmt.Sprintf("(cni:%d observed:%d max:%d)",
			len(cniInterfacesV1(network)), len(network.ObservedInterfaces), count)))).SetSelectable(true).SetExpanded(true)

	if len(cniInterfacesV1(network)) == 0 && len(network.ObservedInterfaces) == 0 {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted("No interface data"))).SetSelectable(false))
		return node
	}

	if len(cniInterfacesV1(network)) > 0 {
		node.AddChild(buildCNIInterfacesNodeV1(network.CNI.Interfaces))
	}
	if len(network.ObservedInterfaces) > 0 {
		node.AddChild(buildObservedInterfacesNodeV1(network.ObservedInterfaces))
	}
	return node
}

func buildCNIInterfacesNodeV1(interfaces []*runtime.CNIInterface) *tview.TreeNode {
	node := tview.NewTreeNode(components.Accent(fmt.Sprintf("  CNI Interfaces (%d)", len(interfaces)))).SetSelectable(true).SetExpanded(false)
	sorted := make([]*runtime.CNIInterface, len(interfaces))
	copy(sorted, interfaces)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	for _, iface := range sorted {
		ifaceNode := tview.NewTreeNode(iface.Name).SetSelectable(true).SetExpanded(false)
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Source:"), components.Bright("cni"))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("MAC:"), components.Bright(fallbackNetField(iface.MAC)))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Sandbox:"), components.Bright(fallbackNetField(iface.Sandbox)))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("PCI:"), components.Bright(fallbackNetField(iface.PciID)))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Socket:"), components.Bright(fallbackNetField(iface.SocketPath)))).SetSelectable(true))
		if len(iface.Addresses) == 0 {
			ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Addresses:"), components.Bright("-"))).SetSelectable(true))
		} else {
			for _, addr := range iface.Addresses {
				ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s  %s %s  %s %s",
					components.Muted("Address:"), components.Bright(fallbackNetField(addr.CIDR)),
					components.Muted("Gateway:"), components.Bright(fallbackNetField(addr.Gateway)),
					components.Muted("Family:"), components.Bright(fallbackNetField(addr.Family)))).SetSelectable(true))
			}
		}
		node.AddChild(ifaceNode)
	}
	return node
}

func buildObservedInterfacesNodeV1(interfaces []*runtime.NetworkStats) *tview.TreeNode {
	node := tview.NewTreeNode(components.Accent(fmt.Sprintf("  Observed Traffic (%d)", len(interfaces)))).SetSelectable(true).SetExpanded(true)

	observed := make([]*runtime.NetworkStats, len(interfaces))
	copy(observed, interfaces)
	sort.SliceStable(observed, func(i, j int) bool {
		return observed[i].Interface < observed[j].Interface
	})
	for _, iface := range observed {
		ifaceNode := tview.NewTreeNode(iface.Interface).SetSelectable(true).SetExpanded(false)
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Source:"), components.Bright("procfs"))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s %s",
			components.Muted("RX:"), components.Bright(formatBytes(int64(iface.RxBytes))),
			components.Muted(fmt.Sprintf("(%d packets, %s)", iface.RxPackets, formatRate(iface.RxBytesPerSec))))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s %s",
			components.Muted("TX:"), components.Bright(formatBytes(int64(iface.TxBytes))),
			components.Muted(fmt.Sprintf("(%d packets, %s)", iface.TxPackets, formatRate(iface.TxBytesPerSec))))).SetSelectable(true))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s",
			components.Muted("Errors:"), components.Bright(fmt.Sprintf("rx=%d tx=%d", iface.RxErrors, iface.TxErrors)))).SetSelectable(true))
		node.AddChild(ifaceNode)
	}
	return node
}

func cniInterfacesV1(network *runtime.PodNetworkInfo) []*runtime.CNIInterface {
	if network == nil || network.CNI == nil {
		return nil
	}
	return network.CNI.Interfaces
}

func buildRoutesNodeV1(cni *runtime.CNIResultInfo) *tview.TreeNode {
	count := 0
	if cni != nil {
		count = len(cni.Routes)
	}
	node := tview.NewTreeNode(components.Accent(fmt.Sprintf("CNI Routes (%d)", count))).SetSelectable(true).SetExpanded(false)
	if cni == nil || len(cni.Routes) == 0 {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted("No CNI route data"))).SetSelectable(false))
		return node
	}
	for _, route := range cni.Routes {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s",
			components.Muted(fmt.Sprintf("%s -> %s", fallbackNetField(route.Destination), fallbackNetField(route.Gateway))))).SetSelectable(true))
	}
	return node
}

func cniDNSV1(cni *runtime.CNIResultInfo) *runtime.DNSConfig {
	if cni == nil {
		return nil
	}
	return cni.DNS
}

func fallbackNetField(value string) string {
	return fallbackValue(value, "-")
}

func nonEmptyOrDashV1(values []string) []string {
	if len(values) == 0 {
		return []string{"-"}
	}
	items := make([]string, 0, len(values))
	for _, v := range values {
		items = append(items, fallbackNetField(v))
	}
	return items
}

func selectFirstNetworkNode(root *tview.TreeNode) *tview.TreeNode {
	if root == nil {
		return nil
	}
	children := root.GetChildren()
	if len(children) > 0 {
		return children[0]
	}
	return root
}
