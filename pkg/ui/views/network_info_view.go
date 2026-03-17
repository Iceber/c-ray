package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
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
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No network data[-]").SetSelectable(false))
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
		return err
	}

	v.render(netState)
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *NetworkInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	}
	switch event.Rune() {
	case 'e', 'E':
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	case 'a', 'A':
		v.expandAll()
		return nil
	}
	return event
}

// GetFocusPrimitive returns the tree focus target.
func (v *NetworkInfoView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func (v *NetworkInfoView) renderEmpty() {
	root := tview.NewTreeNode("[aqua::b]Network[-:-:-]").SetSelectable(false).SetExpanded(true)
	root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve sandbox, DNS, interfaces and routes[-]").SetSelectable(false))
	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(root)
}

func (v *NetworkInfoView) render(netState *runtime.ContainerNetworkState) {
	root := tview.NewTreeNode("[aqua::b]Network[-:-:-]").SetSelectable(false).SetExpanded(true)
	if netState == nil || netState.PodNetwork == nil {
		root.AddChild(tview.NewTreeNode("[gray]No network metadata available[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		return
	}

	network := netState.PodNetwork
	root.AddChild(buildSandboxNodeV1(network))
	root.AddChild(buildDNSNodeV1("Sandbox DNS", network.DNS, true))
	root.AddChild(buildInterfacesNodeV1(network))
	root.AddChild(buildRoutesNodeV1(network.CNI))
	root.AddChild(buildDNSNodeV1("CNI DNS", cniDNSV1(network.CNI), false))

	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(selectFirstNetworkNode(root))
}

func (v *NetworkInfoView) expandAll() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
	expand := !root.IsExpanded()
	var walk func(node *tview.TreeNode)
	walk = func(node *tview.TreeNode) {
		node.SetExpanded(expand)
		for _, child := range node.GetChildren() {
			walk(child)
		}
	}
	walk(root)
	root.SetExpanded(true)
	v.tree.SetCurrentNode(root)
}

func (v *NetworkInfoView) updateStatusBar() {
	v.statusBar.SetText(" [white]Network:[-] sandbox, CNI metadata and observed traffic  |  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

// --- Builder helpers ---

func buildSandboxNodeV1(network *runtime.PodNetworkInfo) *tview.TreeNode {
	node := tview.NewTreeNode("[yellow::b]Sandbox[-:-:-]").SetSelectable(true).SetExpanded(true)
	rows := []string{
		"Sandbox ID: " + fallbackNetField(network.SandboxID),
		"State: " + fallbackNetField(network.SandboxState),
		"Primary IP: " + fallbackNetField(network.PrimaryIP),
		"Additional IPs: " + strings.Join(nonEmptyOrDashV1(network.AdditionalIPs), ", "),
		fmt.Sprintf("Host Network: %t", network.HostNetwork),
		"Namespace Mode: " + fallbackNetField(network.NamespaceMode),
		"NetNS Path: " + fallbackNetField(network.NetNSPath),
		"Hostname: " + fallbackNetField(network.Hostname),
		"Runtime Handler: " + fallbackNetField(network.RuntimeHandler),
		"Runtime Type: " + fallbackNetField(network.RuntimeType),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	if len(network.PortMappings) > 0 {
		portsNode := tview.NewTreeNode(fmt.Sprintf("[aqua::b]  Port Mappings (%d)[-:-:-]", len(network.PortMappings))).
			SetSelectable(true).SetExpanded(false)
		for _, port := range network.PortMappings {
			portsNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]    %s:%d -> %d/%s[-]",
				fallbackNetField(port.HostIP), port.HostPort, port.ContainerPort, strings.ToLower(port.Protocol))).SetSelectable(false))
		}
		node.AddChild(portsNode)
	}
	if len(network.Warnings) > 0 {
		warningsNode := tview.NewTreeNode(fmt.Sprintf("[yellow::b]  Warnings (%d)[-:-:-]", len(network.Warnings))).
			SetSelectable(true).SetExpanded(false)
		for _, warning := range network.Warnings {
			warningsNode.AddChild(tview.NewTreeNode("[gray]    " + warning + "[-]").SetSelectable(false))
		}
		node.AddChild(warningsNode)
	}
	return node
}

func buildDNSNodeV1(title string, dns *runtime.DNSConfig, expanded bool) *tview.TreeNode {
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]%s[-:-:-]", title)).SetSelectable(true).SetExpanded(expanded)
	if dns == nil {
		node.AddChild(tview.NewTreeNode("[gray]  No DNS data[-]").SetSelectable(false))
		return node
	}
	rows := []string{
		"Domain: " + fallbackNetField(dns.Domain),
		"Servers: " + strings.Join(nonEmptyOrDashV1(dns.Servers), ", "),
		"Searches: " + strings.Join(nonEmptyOrDashV1(dns.Searches), ", "),
		"Options: " + strings.Join(nonEmptyOrDashV1(dns.Options), ", "),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	return node
}

func buildInterfacesNodeV1(network *runtime.PodNetworkInfo) *tview.TreeNode {
	count := len(network.ObservedInterfaces)
	if network.CNI != nil && len(network.CNI.Interfaces) > count {
		count = len(network.CNI.Interfaces)
	}

	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]Interfaces[-:-:-] [gray](cni:%d observed:%d max:%d)[-]",
		len(cniInterfacesV1(network)), len(network.ObservedInterfaces), count)).SetSelectable(true).SetExpanded(true)

	if len(cniInterfacesV1(network)) == 0 && len(network.ObservedInterfaces) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]  No interface data[-]").SetSelectable(false))
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
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]  CNI Interfaces (%d)[-:-:-]", len(interfaces))).SetSelectable(true).SetExpanded(false)
	sorted := make([]*runtime.CNIInterface, len(interfaces))
	copy(sorted, interfaces)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	for _, iface := range sorted {
		ifaceNode := tview.NewTreeNode(iface.Name).SetSelectable(true).SetExpanded(false)
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  Source: [white]cni[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  MAC: [white]" + fallbackNetField(iface.MAC) + "[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  Sandbox: [white]" + fallbackNetField(iface.Sandbox) + "[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  PCI: [white]" + fallbackNetField(iface.PciID) + "[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  Socket: [white]" + fallbackNetField(iface.SocketPath) + "[-]").SetSelectable(false))
		if len(iface.Addresses) == 0 {
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  Addresses: [white]-[-]").SetSelectable(false))
		} else {
			for _, addr := range iface.Addresses {
				ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Address: [white]%s[-]  [gray]Gateway:[-] [white]%s[-]  [gray]Family:[-] [white]%s[-]",
					fallbackNetField(addr.CIDR), fallbackNetField(addr.Gateway), fallbackNetField(addr.Family))).SetSelectable(false))
			}
		}
		node.AddChild(ifaceNode)
	}
	return node
}

func buildObservedInterfacesNodeV1(interfaces []*runtime.NetworkStats) *tview.TreeNode {
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]  Observed Traffic (%d)[-:-:-]", len(interfaces))).SetSelectable(true).SetExpanded(true)

	observed := make([]*runtime.NetworkStats, len(interfaces))
	copy(observed, interfaces)
	sort.SliceStable(observed, func(i, j int) bool {
		return observed[i].Interface < observed[j].Interface
	})
	for _, iface := range observed {
		ifaceNode := tview.NewTreeNode(iface.Interface).SetSelectable(true).SetExpanded(false)
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  Source: [white]procfs[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  RX: [white]%s[-] [gray](%d packets, %s)[-]",
			formatBytes(int64(iface.RxBytes)), iface.RxPackets, formatRate(iface.RxBytesPerSec))).SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  TX: [white]%s[-] [gray](%d packets, %s)[-]",
			formatBytes(int64(iface.TxBytes)), iface.TxPackets, formatRate(iface.TxBytesPerSec))).SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Errors: [white]rx=%d tx=%d[-]", iface.RxErrors, iface.TxErrors)).SetSelectable(false))
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
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]CNI Routes (%d)[-:-:-]", count)).SetSelectable(true).SetExpanded(false)
	if cni == nil || len(cni.Routes) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]  No CNI route data[-]").SetSelectable(false))
		return node
	}
	for _, route := range cni.Routes {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  %s -> %s[-]",
			fallbackNetField(route.Destination), fallbackNetField(route.Gateway))).SetSelectable(false))
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
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
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
