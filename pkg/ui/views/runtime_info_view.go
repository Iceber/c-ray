package views

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// RuntimeInfoView displays container runtime information
type RuntimeInfoView struct {
	*tview.Flex

	infoPanel *components.InfoPanel
	statusBar *tview.TextView

	detail *models.ContainerDetail
	mu     sync.Mutex
}

// NewRuntimeInfoView creates a new runtime info view
func NewRuntimeInfoView() *RuntimeInfoView {
	v := &RuntimeInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
	}

	v.infoPanel = components.NewInfoPanel()
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.infoPanel, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.updateStatusBar()
	return v
}

// SetDetail sets the container detail and renders
func (v *RuntimeInfoView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
}

// render updates the info panel with runtime data
func (v *RuntimeInfoView) render() {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	if detail == nil {
		return
	}

	sections := []components.InfoSection{}
	profile := detail.RuntimeProfile

	// Section 1: Runtime Process
	processSection := components.InfoSection{
		Title: "Runtime Process",
		Items: []components.InfoItem{
			{Label: "Host PID:", Value: fmt.Sprintf("%d", detail.PID)},
		},
	}
	if detail.ShimPID > 0 {
		processSection.Items = append(processSection.Items, components.InfoItem{
			Label: "Shim PID:", Value: fmt.Sprintf("%d", detail.ShimPID),
		})
	}
	if profile != nil && profile.Shim != nil {
		if profile.Shim.BinaryPath != "" {
			processSection.Items = append(processSection.Items, components.InfoItem{
				Label: "Shim Binary:", Value: profile.Shim.BinaryPath,
			})
		}
		if profile.Shim.SocketAddress != "" {
			processSection.Items = append(processSection.Items, components.InfoItem{
				Label: "Shim Socket:", Value: profile.Shim.SocketAddress,
			})
		}
		if len(profile.Shim.Cmdline) > 0 {
			processSection.Items = append(processSection.Items, components.InfoItem{
				Label: "Shim Cmdline:", Value: strings.Join(profile.Shim.Cmdline, " "),
			})
		}
	}
	sections = append(sections, processSection)

	// Section 2: OCI Runtime
	if profile != nil && profile.OCI != nil {
		ociItems := []components.InfoItem{}
		if profile.OCI.RuntimeName != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "Runtime Name:", Value: profile.OCI.RuntimeName,
			})
		}
		if profile.OCI.RuntimeBinary != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "Runtime Binary:", Value: profile.OCI.RuntimeBinary,
			})
		}
		if profile.OCI.BundleDir != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "OCI Bundle:", Value: profile.OCI.BundleDir,
			})
		}
		if profile.OCI.StateDir != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "OCI State Dir:", Value: profile.OCI.StateDir,
			})
		}
		if profile.OCI.ConfigPath != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "OCI Spec Config:", Value: profile.OCI.ConfigPath,
			})
		}
		if profile.OCI.SandboxID != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "Sandbox ID:", Value: profile.OCI.SandboxID,
			})
		}
		sections = append(sections, components.InfoSection{Title: "OCI Runtime", Items: ociItems})
	}

	// Section 3: Namespaces
	if len(detail.Namespaces) > 0 {
		nsItems := []components.InfoItem{}
		for nsType, nsPath := range detail.Namespaces {
			nsItems = append(nsItems, components.InfoItem{
				Label: nsType + ":", Value: nsPath, Color: tcell.ColorDarkCyan,
			})
		}
		sections = append(sections, components.InfoSection{Title: "Namespaces", Items: nsItems})
	}

	// Section 4: Network
	if detail.IPAddress != "" || len(detail.PortMappings) > 0 {
		netItems := []components.InfoItem{}
		if detail.IPAddress != "" {
			netItems = append(netItems, components.InfoItem{
				Label: "IP Address:", Value: detail.IPAddress, Color: tcell.ColorGreen,
			})
		}
		for _, pm := range detail.PortMappings {
			netItems = append(netItems, components.InfoItem{
				Label: "Port Mapping:", Value: fmt.Sprintf("%s:%d -> %d/%s",
					pm.HostIP, pm.HostPort, pm.ContainerPort, pm.Protocol),
			})
		}
		sections = append(sections, components.InfoSection{Title: "Network", Items: netItems})
	}

	// Section 5: Labels
	if len(detail.Labels) > 0 {
		labelItems := []components.InfoItem{}
		for k, val := range detail.Labels {
			label := k
			if len(label) > 40 {
				label = label[:37] + "..."
			}
			value := val
			if len(value) > 60 {
				value = value[:57] + "..."
			}
			labelItems = append(labelItems, components.InfoItem{
				Label: label + ":", Value: value, Color: tcell.ColorGray,
			})
		}
		sections = append(sections, components.InfoSection{Title: "Labels", Items: labelItems})
	}

	// Section 6: CGroup
	if profile != nil && profile.CGroup != nil && (profile.CGroup.RelativePath != "" || profile.CGroup.AbsolutePath != "") {
		cgItems := []components.InfoItem{
			{Label: "Relative Path:", Value: profile.CGroup.RelativePath},
			{Label: "Absolute Path:", Value: profile.CGroup.AbsolutePath},
			{Label: "Driver:", Value: profile.CGroup.Driver},
			{Label: "Version:", Value: fmt.Sprintf("v%d", profile.CGroup.Version)},
		}
		if detail.CGroupLimits != nil {
			limits := detail.CGroupLimits
			parts := []string{}
			if limits.CPUQuota > 0 && limits.CPUPeriod > 0 {
				parts = append(parts, fmt.Sprintf("CPU: %.2f cores", float64(limits.CPUQuota)/float64(limits.CPUPeriod)))
			}
			if limits.MemoryLimit > 0 {
				parts = append(parts, fmt.Sprintf("Mem: %s", formatBytes(limits.MemoryLimit)))
			}
			if limits.PidsLimit > 0 {
				parts = append(parts, fmt.Sprintf("PIDs: %d", limits.PidsLimit))
			}
			if len(parts) > 0 {
				cgItems = append(cgItems, components.InfoItem{
					Label: "Limits:", Value: strings.Join(parts, ", "),
				})
			}
		}
		sections = append(sections, components.InfoSection{Title: "CGroup", Items: cgItems})
	}

	// Section 7: RootFS
	if profile != nil && profile.RootFS != nil && (profile.RootFS.BundleRootFSPath != "" || profile.RootFS.MountRootFSPath != "") {
		rootFSItems := []components.InfoItem{}
		if profile.RootFS.BundleRootFSPath != "" {
			rootFSItems = append(rootFSItems, components.InfoItem{
				Label: "Bundle RootFS:", Value: profile.RootFS.BundleRootFSPath,
			})
		}
		if profile.RootFS.MountRootFSPath != "" {
			rootFSItems = append(rootFSItems, components.InfoItem{
				Label: "Mounted RootFS:", Value: profile.RootFS.MountRootFSPath,
			})
		}
		if detail.Snapshotter != "" {
			rootFSItems = append(rootFSItems, components.InfoItem{
				Label: "Snapshotter:", Value: detail.Snapshotter,
			})
		}
		sections = append(sections, components.InfoSection{Title: "RootFS", Items: rootFSItems})
	}

	v.infoPanel.SetSections(sections)
}

// HandleInput processes key events
func (v *RuntimeInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	return event
}

// GetFocusPrimitive returns the focusable primitive (infoPanel)
func (v *RuntimeInfoView) GetFocusPrimitive() tview.Primitive {
	return v.infoPanel
}

// updateStatusBar renders the status bar
func (v *RuntimeInfoView) updateStatusBar() {
	v.statusBar.SetText(" [white]Runtime information  |  [yellow]r[white]:refresh")
}
