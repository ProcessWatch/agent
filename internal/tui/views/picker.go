package views

import (
	"context"
	"fmt"
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
	fieldRestartCmd = iota
	fieldMaxRetries
	fieldCooldownSecs
	fieldAutoRestart
	fieldCount
)

var fieldLabels = [fieldCount]string{
	"Restart command     ",
	"Max retries         ",
	"Cooldown (secs)     ",
	"Auto-restart        ",
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

	m.inputs[fieldRestartCmd] = newInput("e.g. notepad.exe", m.selected.Name, 256)
	m.inputs[fieldMaxRetries] = newInput("5", "5", 3)
	m.inputs[fieldCooldownSecs] = newInput("10", "10", 4)
	m.inputs[fieldAutoRestart] = newInput("true", "true", 5)

	m.focused = fieldRestartCmd
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
	case "tab", "down":
		m.inputs[m.focused].Blur()
		m.focused = (m.focused + 1) % fieldCount
		m.inputs[m.focused].Focus()
		return m, nil
	case "shift+tab", "up":
		m.inputs[m.focused].Blur()
		m.focused = (m.focused - 1 + fieldCount) % fieldCount
		m.inputs[m.focused].Focus()
		return m, nil
	case "enter":
		if m.focused < fieldCount-1 {
			m.inputs[m.focused].Blur()
			m.focused++
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
	maxRetries, _ := strconv.Atoi(strings.TrimSpace(m.inputs[fieldMaxRetries].Value()))
	cooldownSecs, _ := strconv.Atoi(strings.TrimSpace(m.inputs[fieldCooldownSecs].Value()))
	autoRestart := strings.EqualFold(strings.TrimSpace(m.inputs[fieldAutoRestart].Value()), "true")

	if maxRetries <= 0 {
		maxRetries = 5
	}
	if cooldownSecs <= 0 {
		cooldownSecs = 10
	}

	entry := core.WatchlistItem{
		Name:         m.selected.Name,
		RestartCmd:   strings.TrimSpace(m.inputs[fieldRestartCmd].Value()),
		AutoRestart:  autoRestart,
		MaxRetries:   maxRetries,
		CooldownSecs: cooldownSecs,
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

	for i := range fieldCount {
		label := fieldLabels[i]
		if i == m.focused {
			b.WriteString(activeStyle.Render(label+": ") + m.inputs[i].View())
		} else {
			b.WriteString(styleDim.Render(label+": ") + m.inputs[i].View())
		}
		b.WriteString("\n\n")
	}

	if m.err != "" {
		b.WriteString(styleStopped.Render("error: " + m.err))
		b.WriteString("\n\n")
	}

	b.WriteString(styleDim.Render("tab/↑↓ navigate · enter next/confirm · esc back"))

	return styleBorder.Width(m.width - 4).Render(b.String())
}
