package views

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// DetailSummaryView renders the Summary tab for container detail.
// Uses a card-based dual-column layout instead of a tree.
type DetailSummaryView struct {
	*tview.Flex

	app        *tview.Application
	kpiStrip   *tview.TextView
	leftPanel  *tview.TextView
	rightPanel *tview.TextView
	container  runtime.Container
	mu         sync.Mutex
}

// NewDetailSummaryView creates a new detail summary view.
func NewDetailSummaryView(app *tview.Application) *DetailSummaryView {
	v := &DetailSummaryView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	// KPI strip — top metrics bar
	v.kpiStrip = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.kpiStrip.SetBackgroundColor(components.ColorBgHeader)

	// Left panel — Status / Lifecycle
	v.leftPanel = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.leftPanel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.leftPanel.SetTitle(fmt.Sprintf(" %s ", components.Accent("Status & Lifecycle"))).SetTitleAlign(tview.AlignLeft)
	v.leftPanel.SetBackgroundColor(components.ColorBg)

	// Right panel — Image / Runtime / Pod
	v.rightPanel = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.rightPanel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.rightPanel.SetTitle(fmt.Sprintf(" %s ", components.Accent("Image & Runtime"))).SetTitleAlign(tview.AlignLeft)
	v.rightPanel.SetBackgroundColor(components.ColorBg)

	// Dual-column body
	body := tview.NewFlex().SetDirection(tview.FlexColumn)
	body.AddItem(v.leftPanel, 0, 1, true)
	body.AddItem(v.rightPanel, 0, 1, false)

	v.Flex.AddItem(v.kpiStrip, 1, 0, false)
	v.Flex.AddItem(body, 0, 1, true)
	return v
}

// SetContainer sets the container handle.
func (v *DetailSummaryView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
}

// Refresh loads summary data from the container handle.
func (v *DetailSummaryView) Refresh(ctx context.Context) {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return
	}

	info, _ := c.Info(ctx)
	state, _ := c.State(ctx)
	config, _ := c.Config(ctx)
	profile, _ := c.Runtime(ctx)

	var imageConfig *runtime.ImageConfigInfo
	if img, err := c.Image(ctx); err == nil && img != nil {
		imageConfig, _ = img.Config(ctx)
	}

	v.render(info, state, config, profile, imageConfig)
}

// GetFocusPrimitive returns the focusable primitive.
func (v *DetailSummaryView) GetFocusPrimitive() tview.Primitive {
	return v.leftPanel
}

// HandleInput processes local keybindings.
func (v *DetailSummaryView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	return event
}

func (v *DetailSummaryView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		v.kpiStrip.SetText(fmt.Sprintf(" %s", components.Muted("Loading container summary...")))
		v.leftPanel.SetText("")
		v.rightPanel.SetText("")
	})
}

func (v *DetailSummaryView) render(info *runtime.ContainerInfo, state *runtime.ContainerState, config *runtime.ContainerConfig, rt *runtime.RuntimeProfile, imageConfig *runtime.ImageConfigInfo) {
	// Build KPI strip
	kpis := buildKPIStrip(info, state, config, rt)

	// Build left panel (Status & Lifecycle)
	left := buildStatusPanel(info, state)

	// Build right panel (Image & Runtime & Pod)
	right := buildImageRuntimePanel(info, config, rt, imageConfig)

	queueUpdateDraw(v.app, func() {
		v.kpiStrip.SetText(kpis)
		v.leftPanel.SetText(left)
		v.rightPanel.SetText(right)
	})
}

// --- KPI Strip ---

func buildKPIStrip(info *runtime.ContainerInfo, state *runtime.ContainerState, config *runtime.ContainerConfig, rt *runtime.RuntimeProfile) string {
	cards := []string{}

	// PID
	pid := "-"
	if state != nil && state.PID > 0 {
		pid = fmt.Sprintf("%d", state.PID)
	} else if info != nil && info.PID > 0 {
		pid = fmt.Sprintf("%d", info.PID)
	}
	cards = append(cards, kpiCard("PID", pid))

	// Uptime
	uptime := "-"
	if state != nil && !state.StartedAt.IsZero() && state.Status == runtime.ContainerStatusRunning {
		uptime = formatAge(state.StartedAt)
	}
	cards = append(cards, kpiCard("Uptime", uptime))

	// Restarts
	restarts := "-"
	if state != nil && state.RestartCount != nil {
		restarts = fmt.Sprintf("%d", *state.RestartCount)
	}
	cards = append(cards, kpiCard("Restarts", restarts))

	// Exit Code
	exitCode := "-"
	if state != nil && state.ExitCode != nil {
		exitCode = fmt.Sprintf("%d", *state.ExitCode)
	}
	cards = append(cards, kpiCard("Exit", exitCode))

	// Image
	imageName := "-"
	if config != nil && config.ImageRef != "" {
		imageName = truncateForCard(config.ImageRef, 28)
	} else if info != nil && info.ImageRef != "" {
		imageName = truncateForCard(info.ImageRef, 28)
	}
	cards = append(cards, kpiCard("Image", imageName))

	// Sandbox
	sandboxID := resolveSandboxID(rt)
	sandbox := "-"
	if sandboxID != "" {
		sandbox = shortID(sandboxID)
	}
	cards = append(cards, kpiCard("Sandbox", sandbox))

	return " " + strings.Join(cards, "  "+components.Dim("|")+"  ")
}

func kpiCard(label, value string) string {
	return fmt.Sprintf("%s %s", components.Muted(label+":"), components.Bright(value))
}

// --- Left Panel: Status & Lifecycle ---

func buildStatusPanel(info *runtime.ContainerInfo, state *runtime.ContainerState) string {
	var lines []string

	status := "unknown"
	if state != nil {
		status = string(state.Status)
	} else if info != nil {
		status = string(info.Status)
	}
	lines = append(lines, summaryKV("Status", status))

	if state != nil && state.PID > 0 {
		lines = append(lines, summaryKV("PID", fmt.Sprintf("%d", state.PID)))
	}
	if state != nil && state.PPID > 0 {
		lines = append(lines, summaryKV("Shim PID", fmt.Sprintf("%d", state.PPID)))
	}
	if info != nil {
		lines = append(lines, summaryKV("Created At", formatSummaryTime(info.CreatedAt)))
	}
	if state != nil {
		lines = append(lines, summaryKV("Started At", formatSummaryTime(state.StartedAt)))
		if state.Status == runtime.ContainerStatusStopped {
			lines = append(lines, summaryKV("Exited At", formatSummaryTime(state.ExitedAt)))
			exitCode := "unknown"
			if state.ExitCode != nil {
				exitCode = fmt.Sprintf("%d", *state.ExitCode)
			}
			lines = append(lines, summaryKV("Exit Code", exitCode))
			lines = append(lines, summaryKV("Exit Reason", fallbackValue(state.ExitReason, "unknown")))
		}
		restarts := "unknown"
		if state.RestartCount != nil {
			restarts = fmt.Sprintf("%d", *state.RestartCount)
		}
		lines = append(lines, summaryKV("Restart Count", restarts))
	}

	return strings.Join(lines, "\n")
}

// --- Right Panel: Image & Runtime ---

func buildImageRuntimePanel(info *runtime.ContainerInfo, config *runtime.ContainerConfig, rt *runtime.RuntimeProfile, imageConfig *runtime.ImageConfigInfo) string {
	var lines []string

	// Image section
	imageName := "unknown"
	if config != nil && config.ImageRef != "" {
		imageName = config.ImageRef
	} else if info != nil && info.ImageRef != "" {
		imageName = info.ImageRef
	}
	lines = append(lines, summaryKV("Image", imageName))

	imageID := "unknown"
	if info != nil && info.ImageID != "" {
		imageID = info.ImageID
	} else if info != nil && info.ImageRef != "" {
		imageID = info.ImageRef
	}
	lines = append(lines, summaryKV("Image ID", imageID))

	if imageConfig != nil {
		mediaType := "unknown"
		if imageConfig.TargetKind != "" || imageConfig.Schema != "" {
			mediaType = strings.TrimSpace(imageConfig.TargetKind + " / " + imageConfig.Schema)
			mediaType = strings.Trim(mediaType, " /")
		}
		lines = append(lines, summaryKV("Media Type", mediaType))
		if imageConfig.Manifest != nil {
			if platform := imageCurrentPlatform(imageConfig); platform != "" {
				lines = append(lines, summaryKV("Platform", platform))
			}
			if platforms := imagePlatformsSummary(imageConfig); platforms != "" {
				lines = append(lines, summaryKV("Platforms", platforms))
			}
			if imageConfig.Manifest.Digest != "" {
				lines = append(lines, summaryKV("Manifest", shortID(imageConfig.Manifest.Digest)))
			}
			if imageConfig.IndexPath != "" {
				lines = append(lines, summaryKV("Index Path", imageConfig.IndexPath))
			}
			if imageConfig.Manifest.ConfigPath != "" {
				lines = append(lines, summaryKV("Config Path", imageConfig.Manifest.ConfigPath))
			}
		}
	}

	lines = append(lines, "")

	// Pod section
	if info != nil && (info.PodNamespace != "" || info.PodName != "" || info.PodUID != "") {
		lines = append(lines, fmt.Sprintf("  [%s::b]Pod[-:-:-]", components.ColorName(components.ColorFgAccent)))
		lines = append(lines, summaryKV("Namespace", fallbackValue(info.PodNamespace, "unknown")))
		lines = append(lines, summaryKV("Pod Name", fallbackValue(info.PodName, "unknown")))
		lines = append(lines, summaryKV("Pod UID", fallbackValue(info.PodUID, "unknown")))
		lines = append(lines, "")
	}

	// Sandbox
	sandboxID := resolveSandboxID(rt)
	lines = append(lines, summaryKV("Sandbox ID", fallbackValue(sandboxID, "unknown")))

	// Layer backend
	if config != nil {
		backend := "unknown"
		if config.Backend != nil {
			backend = formatLayerBackendV1(config.Backend)
		}
		lines = append(lines, summaryKV("Layer Backend", backend))
		lines = append(lines, summaryKV("Snapshot Key", fallbackValue(config.SnapshotKey, "unknown")))
	}

	return strings.Join(lines, "\n")
}

func summaryKV(key, value string) string {
	return fmt.Sprintf("  [%s]%-18s[-] [%s]%s[-]", components.ColorName(components.ColorFgMuted), key, components.ColorName(components.ColorFgBright), value)
}

func resolveSandboxID(runtime *runtime.RuntimeProfile) string {
	if runtime != nil && runtime.OCI != nil && runtime.OCI.SandboxID != "" {
		return strings.TrimSpace(runtime.OCI.SandboxID)
	}
	return ""
}
