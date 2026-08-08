package views

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ethan-mdev/process-watch/internal/core"
)

// --- Process list item ---

type processItem struct{ proc core.Process }

func (i processItem) FilterValue() string { return i.proc.Name }
func (i processItem) Title() string {
	return fmt.Sprintf("%-35s PID %d", i.proc.Name, i.proc.PID)
}
func (i processItem) Description() string {
	return fmt.Sprintf("CPU %.1f%%   Mem %.1fMB", i.proc.CPUPercent, i.proc.MemoryMB)
}

// --- Internal messages ---

type pickerProcsLoadedMsg []core.Process
type pickerErrMsg string

// --- Form field indices ---

const (
	fieldMatchMode = iota
	fieldSelector
	fieldExpectedCount
	fieldAutoRestart
	fieldRestartCmd
	fieldMaxRetries
	fieldCooldownSecs
	fieldCount
)

var fieldLabels = [fieldCount]string{
	"Match by            ",
	"Selector            ",
	"Expected instances  ",
	"Auto-restart        ",
	"Restart command     ",
	"Max retries         ",
	"Cooldown (secs)     ",
}

// matchModeHelp explains each mode in terms of what it fixes, since the
// difference between them only matters once something is misidentified.
var matchModeHelp = map[core.MatchMode]string{
	core.MatchSubstring: "Any process whose name contains the selector. Loose — \"node\" also matches \"node_exporter\".",
	core.MatchExact:     "Only processes named exactly this. The safe default.",
	core.MatchCmdline:   "Match part of the full command line. Use this to tell apart several workers that share a name.",
	core.MatchUnit:      "Ask systemd about a unit. Accepts globs like \"gt-web@*\". Most reliable, Linux only.",
}

// availableMatchModes omits unit mode off Linux, where it cannot work.
func availableMatchModes() []core.MatchMode {
	modes := []core.MatchMode{core.MatchExact, core.MatchCmdline, core.MatchSubstring}
	if runtime.GOOS == "linux" {
		modes = append(modes, core.MatchUnit)
	}
	return modes
}

// --- Picker stages ---

type pickerStage int

const (
	stagePicking pickerStage = iota
	stageForm
)

// --- PickerModel ---

type PickerModel struct {
	ctx        context.Context
	processMgr core.ProcessManager
	watchlist  core.WatchlistManager
	stage      pickerStage
	list       list.Model
	inputs     [fieldCount]textinput.Model
	focused    int
	selected   core.Process
	err        string
	width      int
	height     int
}

func NewPickerModel(ctx context.Context, processMgr core.ProcessManager, watchlist core.WatchlistManager) PickerModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa"))
	l.Title = "Select a process to watch  (/ to filter)"
	l.SetShowStatusBar(true)
	return PickerModel{
		ctx:        ctx,
		processMgr: processMgr,
		watchlist:  watchlist,
		list:       l,
	}
}

func (m PickerModel) Init() tea.Cmd {
	return m.loadProcesses()
}

func (m PickerModel) loadProcesses() tea.Cmd {
	return func() tea.Msg {
		procs, err := m.processMgr.ListAll(m.ctx)
		if err != nil {
			return pickerErrMsg(err.Error())
		}
		return pickerProcsLoadedMsg(procs)
	}
}

func (m *PickerModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

func (m PickerModel) initForm() PickerModel {
	newInput := func(placeholder, value string, limit int) textinput.Model {
		t := textinput.New()
		t.Placeholder = placeholder
		t.SetValue(value)
		t.CharLimit = limit
		return t
	}

	// The user just picked a concrete process out of the list, so exact
	// matching on its name is both correct and an immediate upgrade over the
	// substring matching older entries default to.
	m.inputs[fieldMatchMode] = newInput(string(core.MatchExact), string(core.MatchExact), 12)
	m.inputs[fieldSelector] = newInput(m.selected.Name, m.selected.Name, 256)
	m.inputs[fieldExpectedCount] = newInput("1", "1", 4)
	m.inputs[fieldAutoRestart] = newInput("false", "false", 5)
	m.inputs[fieldRestartCmd] = newInput("e.g. systemctl restart my-service", "", 256)
	m.inputs[fieldMaxRetries] = newInput("5", "5", 3)
	m.inputs[fieldCooldownSecs] = newInput("10", "10", 4)

	m.focused = fieldMatchMode
	m.inputs[m.focused].Focus()
	m.stage = stageForm
	m.err = ""
	return m
}

func (m PickerModel) Update(msg tea.Msg) (PickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pickerProcsLoadedMsg:
		items := make([]list.Item, len(msg))
		for i, p := range msg {
			items[i] = processItem{proc: p}
		}
		m.list.SetItems(items)
		return m, nil

	case pickerErrMsg:
		m.err = string(msg)
		return m, nil

	case tea.KeyMsg:
		if m.stage == stagePicking {
			return m.updatePicking(msg)
		}
		return m.updateForm(msg)
	}

	if m.stage == stagePicking {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m PickerModel) updatePicking(msg tea.KeyMsg) (PickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m, func() tea.Msg { return SwitchToListMsg{} }
	case "enter":
		if item, ok := m.list.SelectedItem().(processItem); ok {
			m.selected = item.proc
			m = m.initForm()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m PickerModel) updateForm(msg tea.KeyMsg) (PickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.stage = stagePicking
		return m, nil
	case " ", "left", "right":
		switch m.focused {
		case fieldAutoRestart:
			m.toggleAutoRestart()
			m.err = ""
			return m, nil
		case fieldMatchMode:
			m.cycleMatchMode(msg.String() == "left")
			m.err = ""
			return m, nil
		}
	case "tab", "down":
		m.inputs[m.focused].Blur()
		m.focused = nextVisibleField(m.visibleFields(), m.focused)
		m.inputs[m.focused].Focus()
		return m, nil
	case "shift+tab", "up":
		m.inputs[m.focused].Blur()
		m.focused = previousVisibleField(m.visibleFields(), m.focused)
		m.inputs[m.focused].Focus()
		return m, nil
	case "enter":
		fields := m.visibleFields()
		if m.focused != fields[len(fields)-1] {
			m.inputs[m.focused].Blur()
			m.focused = nextVisibleField(fields, m.focused)
			m.inputs[m.focused].Focus()
			return m, nil
		}
		return m.submitForm()
	}

	var cmd tea.Cmd
	m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
	return m, cmd
}

func (m PickerModel) submitForm() (PickerModel, tea.Cmd) {
	autoRestart := m.autoRestartEnabled()
	restartCmd := strings.TrimSpace(m.inputs[fieldRestartCmd].Value())

	if autoRestart && restartCmd == "" {
		m.err = "restart command is required when auto-restart is enabled"
		return m, nil
	}

	selector := strings.TrimSpace(m.inputs[fieldSelector].Value())
	if selector == "" {
		m.err = "selector cannot be empty"
		return m, nil
	}

	mode := m.matchMode()
	if mode == core.MatchUnit && runtime.GOOS != "linux" {
		m.err = "unit matching requires Linux — use exact or command line instead"
		return m, nil
	}

	expected, ok := parsePositiveInt(m.inputs[fieldExpectedCount].Value(), 1)
	if !ok {
		m.err = "expected instances must be a positive number"
		return m, nil
	}

	maxRetries, ok := parsePositiveInt(m.inputs[fieldMaxRetries].Value(), 5)
	if !ok {
		m.err = "max retries must be a positive number"
		return m, nil
	}
	cooldownSecs, ok := parsePositiveInt(m.inputs[fieldCooldownSecs].Value(), 10)
	if !ok {
		m.err = "cooldown must be a positive number"
		return m, nil
	}

	entry := core.WatchlistItem{
		Name:          m.selected.Name,
		MatchMode:     mode,
		Selector:      selector,
		ExpectedCount: expected,
		RestartCmd:    restartCmd,
		AutoRestart:   autoRestart,
		MaxRetries:    maxRetries,
		CooldownSecs:  cooldownSecs,
	}

	if err := m.watchlist.Add(m.ctx, entry); err != nil {
		m.err = err.Error()
		return m, nil
	}

	return m, func() tea.Msg { return SwitchToListMsg{} }
}

func (m PickerModel) View() string {
	if m.width == 0 {
		return "loading..."
	}
	if m.stage == stagePicking {
		if m.err != "" {
			return m.list.View() + "\n" + styleStopped.Render("error: "+m.err)
		}
		return m.list.View()
	}
	return m.formView()
}

func (m PickerModel) formView() string {
	var b strings.Builder
	b.WriteString(styleBold.Render(fmt.Sprintf(`Add "%s" to watchlist`, m.selected.Name)))
	b.WriteString("\n\n")

	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa")).Bold(true)

	for _, i := range m.visibleFields() {
		label := fieldLabels[i]
		if i == m.focused {
			b.WriteString(activeStyle.Render(label+": ") + m.inputs[i].View())
		} else {
			b.WriteString(styleDim.Render(label+": ") + m.inputs[i].View())
		}
		b.WriteString("\n\n")
	}

	if help, ok := matchModeHelp[m.matchMode()]; ok {
		b.WriteString(styleDim.Render(help))
		b.WriteString("\n\n")
	}

	if !m.autoRestartEnabled() {
		b.WriteString(styleDim.Render("Auto-restart is off. ProcessWatch will monitor this process and report incidents without running a recovery command."))
		b.WriteString("\n\n")
	} else {
		b.WriteString(styleDim.Render("Use a command that works from a plain shell and returns after starting/restarting the service."))
		b.WriteString("\n\n")
	}

	if m.err != "" {
		b.WriteString(styleStopped.Render("error: " + m.err))
		b.WriteString("\n\n")
	}

	b.WriteString(styleDim.Render("tab/↑↓ navigate · space/←→ change · enter next/confirm · esc back"))

	return styleBorder.Width(m.width - 4).Render(b.String())
}

func (m PickerModel) autoRestartEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(m.inputs[fieldAutoRestart].Value()), "true")
}

func (m *PickerModel) toggleAutoRestart() {
	if m.autoRestartEnabled() {
		m.inputs[fieldAutoRestart].SetValue("false")
		return
	}
	m.inputs[fieldAutoRestart].SetValue("true")
}

func (m PickerModel) matchMode() core.MatchMode {
	return core.MatchMode(strings.TrimSpace(m.inputs[fieldMatchMode].Value()))
}

func (m *PickerModel) cycleMatchMode(backwards bool) {
	modes := availableMatchModes()
	current := 0
	for i, mode := range modes {
		if mode == m.matchMode() {
			current = i
			break
		}
	}
	step := 1
	if backwards {
		step = -1
	}
	next := modes[(current+step+len(modes))%len(modes)]
	m.inputs[fieldMatchMode].SetValue(string(next))

	// A unit selector is a unit name, not a process name — seed the suffix so
	// the field is a sensible starting point rather than something that will
	// silently never match.
	if next == core.MatchUnit && !strings.Contains(m.inputs[fieldSelector].Value(), ".") {
		m.inputs[fieldSelector].SetValue(m.selected.Name + ".service")
	}
}

func (m PickerModel) visibleFields() []int {
	fields := []int{fieldMatchMode, fieldSelector, fieldExpectedCount, fieldAutoRestart}
	if !m.autoRestartEnabled() {
		return fields
	}
	return append(fields, fieldRestartCmd, fieldMaxRetries, fieldCooldownSecs)
}

// parsePositiveInt parses s as a positive integer, returning fallback when s
// is empty. ok is false when s is non-empty and not a positive integer.
func parsePositiveInt(s string, fallback int) (value int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func nextVisibleField(fields []int, current int) int {
	for i, field := range fields {
		if field == current {
			return fields[(i+1)%len(fields)]
		}
	}
	return fields[0]
}

func previousVisibleField(fields []int, current int) int {
	for i, field := range fields {
		if field == current {
			return fields[(i-1+len(fields))%len(fields)]
		}
	}
	return fields[0]
}
