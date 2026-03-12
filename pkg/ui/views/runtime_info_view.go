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
	sections = append(sections, processSection)

	// Section 2: OCI Runtime
	if detail.OCIBundlePath != "" || detail.OCIRuntimeDir != "" {
		ociItems := []components.InfoItem{}
		if detail.OCIBundlePath != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "OCI Bundle:", Value: detail.OCIBundlePath,
			})
		}
		if detail.OCIRuntimeDir != "" {
			ociItems = append(ociItems, components.InfoItem{
				Label: "OCI Runtime Dir:", Value: detail.OCIRuntimeDir,
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
	if detail.CGroupPath != "" {
		cgItems := []components.InfoItem{
			{Label: "Path:", Value: detail.CGroupPath},
			{Label: "Version:", Value: fmt.Sprintf("v%d", detail.CGroupVersion)},
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
