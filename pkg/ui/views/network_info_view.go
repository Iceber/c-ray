package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// NetworkInfoView renders the Network page.
type NetworkInfoView struct {
	*tview.Flex

	rt          runtime.Runtime
	tree        *tview.TreeView
	statusBar   *tview.TextView
	containerID string
	detail      *models.ContainerDetail
	mu          sync.Mutex
}

// NewNetworkInfoView creates a new network info view.
func NewNetworkInfoView(rt runtime.Runtime) *NetworkInfoView {
	v := &NetworkInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		rt:   rt,
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

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the active container.
func (v *NetworkInfoView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.detail = nil
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// Refresh loads runtime network metadata.
func (v *NetworkInfoView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	containerID := v.containerID
	v.mu.Unlock()
	if strings.TrimSpace(containerID) == "" {
		v.render()
		return nil
	}

	detail, err := v.rt.GetContainerNetworkInfo(ctx, containerID)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *NetworkInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}
	if event.Key() == tcell.KeyCtrlC {
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

func (v *NetworkInfoView) render() {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	root := tview.NewTreeNode("[aqua::b]Network[-:-:-]").SetSelectable(false).SetExpanded(true)
	if detail == nil || detail.PodNetwork == nil {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve sandbox, DNS, interfaces and CNI routes[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		return
	}

	network := detail.PodNetwork
	root.AddChild(buildNetworkSandboxNode(network))
	root.AddChild(buildNetworkDNSNode("Sandbox DNS", network.DNS, true))
	root.AddChild(buildNetworkInterfacesNode(network))
	root.AddChild(buildNetworkRoutesNode(network.CNI))
	root.AddChild(buildNetworkDNSNode("CNI DNS", cniDNS(network.CNI), false))

	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(root)
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
	v.statusBar.SetText(" [white]Network:[-] sandbox, DNS, CNI interfaces and routes  |  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

func buildNetworkSandboxNode(network *models.PodNetworkInfo) *tview.TreeNode {
	node := tview.NewTreeNode("[yellow::b]Sandbox[-:-:-]").SetSelectable(true).SetExpanded(true)
	rows := []string{
		"Sandbox ID: " + fallbackNetworkField(network.SandboxID),
		"State: " + fallbackNetworkField(network.SandboxState),
		"Primary IP: " + fallbackNetworkField(network.PrimaryIP),
		"Additional IPs: " + strings.Join(nonEmptyOrDash(network.AdditionalIPs), ", "),
		fmt.Sprintf("Host Network: %t", network.HostNetwork),
		"Namespace Mode: " + fallbackNetworkField(network.NamespaceMode),
		"NetNS Path: " + fallbackNetworkField(network.NetNSPath),
		"Hostname: " + fallbackNetworkField(network.Hostname),
		"Runtime Handler: " + fallbackNetworkField(network.RuntimeHandler),
		"Runtime Type: " + fallbackNetworkField(network.RuntimeType),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	if len(network.PortMappings) > 0 {
		portsNode := tview.NewTreeNode(fmt.Sprintf("[aqua::b]  Port Mappings (%d)[-:-:-]", len(network.PortMappings))).SetSelectable(true).SetExpanded(false)
		for _, port := range network.PortMappings {
			portsNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]    %s:%d -> %d/%s[-]", fallbackNetworkField(port.HostIP), port.HostPort, port.ContainerPort, strings.ToLower(port.Protocol))).SetSelectable(false))
		}
		node.AddChild(portsNode)
	}
	if len(network.Warnings) > 0 {
		warningsNode := tview.NewTreeNode(fmt.Sprintf("[yellow::b]  Warnings (%d)[-:-:-]", len(network.Warnings))).SetSelectable(true).SetExpanded(false)
		for _, warning := range network.Warnings {
			warningsNode.AddChild(tview.NewTreeNode("[gray]    " + warning + "[-]").SetSelectable(false))
		}
		node.AddChild(warningsNode)
	}
	return node
}

func buildNetworkDNSNode(title string, dns *models.DNSConfig, expanded bool) *tview.TreeNode {
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]%s[-:-:-]", title)).SetSelectable(true).SetExpanded(expanded)
	if dns == nil {
		node.AddChild(tview.NewTreeNode("[gray]  No DNS data[-]").SetSelectable(false))
		return node
	}
	rows := []string{
		"Domain: " + fallbackNetworkField(dns.Domain),
		"Servers: " + strings.Join(nonEmptyOrDash(dns.Servers), ", "),
		"Searches: " + strings.Join(nonEmptyOrDash(dns.Searches), ", "),
		"Options: " + strings.Join(nonEmptyOrDash(dns.Options), ", "),
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	return node
}

func buildNetworkInterfacesNode(network *models.PodNetworkInfo) *tview.TreeNode {
	cniCount := 0
	observedCount := 0
	if network != nil && network.CNI != nil {
		cniCount = len(network.CNI.Interfaces)
	}
	if network != nil {
		observedCount = len(network.ObservedInterfaces)
	}
	count := cniCount
	if count == 0 {
		count = observedCount
	}
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]Interfaces (%d)[-:-:-]", count)).SetSelectable(true).SetExpanded(true)
	if network == nil {
		node.AddChild(tview.NewTreeNode("[gray]  No interface data[-]").SetSelectable(false))
		return node
	}
	if network.CNI != nil && len(network.CNI.Interfaces) > 0 {
		interfaces := append([]*models.CNIInterface(nil), network.CNI.Interfaces...)
		sort.SliceStable(interfaces, func(i, j int) bool {
			return interfaces[i].Name < interfaces[j].Name
		})
		for _, iface := range interfaces {
			ifaceNode := tview.NewTreeNode(iface.Name).SetSelectable(true).SetExpanded(false)
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  Source: [white]cni[-]").SetSelectable(false))
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  MAC: [white]" + fallbackNetworkField(iface.MAC) + "[-]").SetSelectable(false))
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  Sandbox: [white]" + fallbackNetworkField(iface.Sandbox) + "[-]").SetSelectable(false))
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  PCI: [white]" + fallbackNetworkField(iface.PciID) + "[-]").SetSelectable(false))
			ifaceNode.AddChild(tview.NewTreeNode("[gray]  Socket: [white]" + fallbackNetworkField(iface.SocketPath) + "[-]").SetSelectable(false))
			if len(iface.Addresses) == 0 {
				ifaceNode.AddChild(tview.NewTreeNode("[gray]  Addresses: [white]-[-]").SetSelectable(false))
			} else {
				for _, address := range iface.Addresses {
					ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Address: [white]%s[-]  [gray]Gateway:[-] [white]%s[-]  [gray]Family:[-] [white]%s[-]", fallbackNetworkField(address.CIDR), fallbackNetworkField(address.Gateway), fallbackNetworkField(address.Family))).SetSelectable(false))
				}
			}
			node.AddChild(ifaceNode)
		}
		return node
	}
	if len(network.ObservedInterfaces) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]  No interface data[-]").SetSelectable(false))
		return node
	}

	observed := append([]*models.NetworkStats(nil), network.ObservedInterfaces...)
	sort.SliceStable(observed, func(i, j int) bool {
		return observed[i].Interface < observed[j].Interface
	})
	for _, iface := range observed {
		ifaceNode := tview.NewTreeNode(iface.Interface).SetSelectable(true).SetExpanded(false)
		ifaceNode.AddChild(tview.NewTreeNode("[gray]  Source: [white]procfs[-]").SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  RX: [white]%d bytes / %d packets[-]", iface.RxBytes, iface.RxPackets)).SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  TX: [white]%d bytes / %d packets[-]", iface.TxBytes, iface.TxPackets)).SetSelectable(false))
		ifaceNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Errors: [white]rx=%d tx=%d[-]", iface.RxErrors, iface.TxErrors)).SetSelectable(false))
		node.AddChild(ifaceNode)
	}
	return node
}

func buildNetworkRoutesNode(cni *models.CNIResultInfo) *tview.TreeNode {
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
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  %s -> %s[-]", fallbackNetworkField(route.Destination), fallbackNetworkField(route.Gateway))).SetSelectable(false))
	}
	return node
}

func cniDNS(cni *models.CNIResultInfo) *models.DNSConfig {
	if cni == nil {
		return nil
	}
	return cni.DNS
}

func fallbackNetworkField(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func nonEmptyOrDash(values []string) []string {
	if len(values) == 0 {
		return []string{"-"}
	}
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, fallbackNetworkField(value))
	}
	return items
}
