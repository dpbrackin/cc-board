// Package tui is the cc-board Bubble Tea program: a header with counts, a list
// of sessions grouped by project, an optional preview pane and a help footer.
//
// All I/O (reading files, tmux calls, writing meta files) runs in tea.Cmds;
// Update only changes in-memory state.
package tui

import (
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dpbrackin/cc-board/internal/session"
	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/tmux"
)

// Options configures a Model.
type Options struct {
	Loader          *session.Loader
	Store           *store.Store
	Client          tmux.Client
	PollInterval    time.Duration
	HideClosedAfter time.Duration
	Now             func() time.Time
}

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeRename
)

// row is either a group header or a session in the rendered list.
type row struct {
	header  bool
	group   session.Group
	session session.Session
}

// Model is the Bubble Tea model.
type Model struct {
	opt Options

	sessions []session.Session // everything loaded, unfiltered
	rows     []row
	cursor   int    // index into rows; always a session row when any exist
	selected string // session ID under the cursor, kept across refreshes
	offset   int    // first visible row
	loaded   bool
	loadErr  error

	mode     mode
	showAll  bool
	filter   string
	input    textinput.Model
	renaming string // session ID being renamed

	preview  bool
	viewport viewport.Model

	width, height int
	message       string
	messageErr    bool

	exec *tmux.ExecSpec
}

// Messages.
type (
	tickMsg   struct{}
	loadedMsg struct {
		sessions []session.Session
		err      error
		fromTick bool
	}
	jumpMsg struct {
		outcome tmux.Outcome
		exec    *tmux.ExecSpec
		err     error
	}
	metaSavedMsg struct{ err error }
)

// New returns a Model.
func New(opt Options) Model {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.PollInterval <= 0 {
		opt.PollInterval = 2 * time.Second
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 200
	return Model{opt: opt, input: ti, viewport: viewport.New(0, 0), width: 80, height: 24}
}

// Exec returns the command to exec after the program exits (outside tmux),
// or nil.
func (m Model) Exec() *tmux.ExecSpec { return m.exec }

// Init starts the first load; each load schedules the next poll.
func (m Model) Init() tea.Cmd { return m.loadCmd(true) }

func (m Model) loadCmd(fromTick bool) tea.Cmd {
	loader := m.opt.Loader
	return func() tea.Msg {
		if loader == nil {
			return loadedMsg{fromTick: fromTick}
		}
		s, err := loader.Load()
		return loadedMsg{sessions: s, err: err, fromTick: fromTick}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opt.PollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		return m, m.loadCmd(true)

	case loadedMsg:
		var cmd tea.Cmd
		if msg.fromTick {
			cmd = m.tickCmd()
		}
		m.loaded = true
		m.loadErr = msg.err
		if msg.err == nil {
			m.sessions = msg.sessions
			m.rebuild()
		}
		return m, cmd

	case jumpMsg:
		if msg.err != nil {
			m.setError(msg.err.Error())
			return m, nil
		}
		if msg.outcome == tmux.Exec {
			m.exec = msg.exec
		}
		return m, tea.Quit

	case metaSavedMsg:
		if msg.err != nil {
			m.setError("save failed: " + msg.err.Error())
		}
		return m, m.loadCmd(false)

	case tea.KeyMsg:
		// Fast or scripted input can arrive as one multi-rune message
		// ("jjj", "/flak"). In normal mode, replay it one key at a time so
		// each rune acts as a keybinding and later runes reach any input
		// a key opened.
		if m.mode == modeNormal && msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			var next tea.Model = m
			for _, r := range msg.Runes {
				var cmd tea.Cmd
				next, cmd = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
				cmds = append(cmds, cmd)
			}
			return next, tea.Sequence(cmds...)
		}
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeRename:
			return m.updateRename(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) setError(s string) { m.message, m.messageErr = s, true }
func (m *Model) setInfo(s string)  { m.message, m.messageErr = s, false }

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.message = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.moveTo(0, 1)
	case "G", "end":
		m.moveTo(len(m.rows)-1, -1)
	case "pgdown", "ctrl+d":
		if m.preview {
			m.viewport.HalfViewDown()
		} else {
			m.move(m.listHeight() / 2)
		}
	case "pgup", "ctrl+u":
		if m.preview {
			m.viewport.HalfViewUp()
		} else {
			m.move(-m.listHeight() / 2)
		}
	case "J", "shift+down":
		if m.preview {
			m.viewport.LineDown(1)
		}
	case "K", "shift+up":
		if m.preview {
			m.viewport.LineUp(1)
		}
	case "enter":
		if s, ok := m.current(); ok {
			return m, m.jumpCmd(s)
		}
	case "o":
		if s, ok := m.current(); ok {
			return m, m.openCmd(s)
		}
	case "r":
		if s, ok := m.current(); ok {
			m.mode = modeRename
			m.renaming = s.ID
			m.input.SetValue(s.Label)
			if s.Label == "" {
				m.input.SetValue(s.Title)
				if s.Title == session.Untitled {
					m.input.SetValue("")
				}
			}
			m.input.CursorEnd()
			return m, m.input.Focus()
		}
	case "d":
		if s, ok := m.current(); ok {
			done := !s.Done
			m.patch(s.ID, func(x *session.Session) { x.Done = done })
			return m, m.saveMetaCmd(s.ID, func(mt *store.Meta) { mt.Done = done })
		}
	case "x":
		if s, ok := m.current(); ok {
			hidden := !s.Hidden
			m.patch(s.ID, func(x *session.Session) { x.Hidden = hidden })
			if hidden && !m.showAll {
				m.setInfo("hidden · a shows all")
			}
			return m, m.saveMetaCmd(s.ID, func(mt *store.Meta) { mt.Hidden = hidden })
		}
	case "a":
		m.showAll = !m.showAll
		m.rebuild()
	case "/":
		m.mode = modeFilter
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "p":
		m.preview = !m.preview
		m.layout()
	}
	return m, nil
}

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeNormal
		m.input.Blur()
		m.filter = ""
		m.rebuild()
		return m, nil
	case "enter":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "up", "down":
		if msg.String() == "up" {
			m.move(-1)
		} else {
			m.move(1)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filter = m.input.Value()
	m.rebuild()
	return m, cmd
}

func (m Model) updateRename(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeNormal
		m.input.Blur()
		m.renaming = ""
		return m, nil
	case "enter":
		id := m.renaming
		label := strings.TrimSpace(m.input.Value())
		m.mode = modeNormal
		m.input.Blur()
		m.renaming = ""
		m.patch(id, func(x *session.Session) {
			x.Label = label
			switch {
			case label != "":
				x.Title = label
			case x.FirstPrompt != "":
				x.Title = x.FirstPrompt
			default:
				x.Title = session.Untitled
			}
		})
		return m, m.saveMetaCmd(id, func(mt *store.Meta) { mt.Label = label })
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// patch updates a session in memory so edits show before the next load.
func (m *Model) patch(id string, fn func(*session.Session)) {
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			fn(&m.sessions[i])
		}
	}
	m.rebuild()
}

func (m Model) saveMetaCmd(id string, fn func(*store.Meta)) tea.Cmd {
	st := m.opt.Store
	return func() tea.Msg {
		if st == nil {
			return metaSavedMsg{}
		}
		meta, err := st.ReadMeta(id)
		if err != nil {
			meta = store.Meta{}
		}
		fn(&meta)
		return metaSavedMsg{err: st.WriteMeta(id, meta)}
	}
}

func (m Model) target(s session.Session) tmux.Target {
	return tmux.Target{ID: s.ID, CWD: s.CWD, Pane: s.Pane, Title: s.Title}
}

func (m Model) jumpCmd(s session.Session) tea.Cmd {
	client := m.opt.Client
	t := m.target(s)
	if s.RawStatus == store.StatusEnded {
		t.Pane = "" // an ended session never has a live pane
	}
	return func() tea.Msg {
		out, ex, err := client.Jump(t)
		return jumpMsg{outcome: out, exec: ex, err: friendly(err)}
	}
}

func (m Model) openCmd(s session.Session) tea.Cmd {
	client := m.opt.Client
	dir := s.CWD
	return func() tea.Msg {
		out, ex, err := client.Open(dir)
		return jumpMsg{outcome: out, exec: ex, err: friendly(err)}
	}
}

func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, tmux.ErrNotInTmux):
		return errors.New("not in tmux: can't jump to a live pane (run cc-board inside tmux)")
	case errors.Is(err, tmux.ErrCWDMissing):
		return errors.New("project directory no longer exists")
	}
	return err
}

// current returns the session under the cursor.
func (m Model) current() (session.Session, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].header {
		return session.Session{}, false
	}
	return m.rows[m.cursor].session, true
}

// visible returns the filtered sessions for the current view.
func (m Model) visible() []session.Session {
	vis := session.Visible(m.sessions, m.showAll, m.opt.HideClosedAfter, m.opt.Now())
	if m.filter == "" {
		return vis
	}
	q := strings.ToLower(m.filter)
	out := vis[:0:0]
	for _, s := range vis {
		if strings.Contains(strings.ToLower(s.Title), q) ||
			strings.Contains(strings.ToLower(s.Project()), q) ||
			strings.Contains(strings.ToLower(s.CWD), q) {
			out = append(out, s)
		}
	}
	return out
}

// rebuild regenerates rows and restores the cursor to the selected session.
func (m *Model) rebuild() {
	if s, ok := m.current(); ok {
		m.selected = s.ID
	}
	m.rows = m.rows[:0]
	for _, g := range session.GroupAndSort(m.visible()) {
		m.rows = append(m.rows, row{header: true, group: g})
		for _, s := range g.Sessions {
			m.rows = append(m.rows, row{session: s})
		}
	}
	idx := -1
	for i, r := range m.rows {
		if !r.header && r.session.ID == m.selected {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = m.cursor
		if idx >= len(m.rows) {
			idx = len(m.rows) - 1
		}
		if idx < 0 {
			idx = 0
		}
	}
	m.moveTo(idx, 1)
}

// moveTo puts the cursor on row i, or the nearest session row in direction dir.
func (m *Model) moveTo(i, dir int) {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		m.updatePreview()
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.rows) {
		i = len(m.rows) - 1
	}
	for j := i; j >= 0 && j < len(m.rows); j += dir {
		if !m.rows[j].header {
			m.setCursor(j)
			return
		}
	}
	for j := i; j >= 0 && j < len(m.rows); j -= dir {
		if !m.rows[j].header {
			m.setCursor(j)
			return
		}
	}
}

func (m *Model) setCursor(i int) {
	m.cursor = i
	m.selected = m.rows[i].session.ID
	m.scroll()
	m.updatePreview()
}

// move moves the cursor by n session rows (skipping headers).
func (m *Model) move(n int) {
	if len(m.rows) == 0 || n == 0 {
		return
	}
	dir := 1
	if n < 0 {
		dir, n = -1, -n
	}
	i := m.cursor
	for n > 0 {
		j := i + dir
		for j >= 0 && j < len(m.rows) && m.rows[j].header {
			j += dir
		}
		if j < 0 || j >= len(m.rows) {
			break
		}
		i = j
		n--
	}
	m.setCursor(i)
}

// scroll keeps the cursor (and its group header) inside the list window.
func (m *Model) scroll() {
	h := m.listHeight()
	if h <= 0 {
		return
	}
	top := m.cursor
	if top > 0 && m.rows[top-1].header {
		top--
	}
	if top < m.offset {
		m.offset = top
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if max := len(m.rows) - h; m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) layout() {
	m.viewport.Width = m.width - 2
	m.viewport.Height = m.previewHeight()
	m.scroll()
	m.updatePreview()
}

func (m *Model) updatePreview() {
	if !m.preview {
		return
	}
	s, ok := m.current()
	text := ""
	if ok {
		text = s.LastAssistant
		if text == "" {
			text = "(no assistant message yet)"
		}
	}
	w := m.viewport.Width
	if w < 10 {
		w = 10
	}
	m.viewport.SetContent(wrap(text, w))
	m.viewport.GotoTop()
}

// Layout: header, rule, list, [rule, preview], rule, footer.
func (m Model) previewHeight() int {
	if !m.preview {
		return 0
	}
	h := (m.height - 4) * 2 / 5
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) listHeight() int {
	h := m.height - 4
	if m.preview {
		h -= m.previewHeight() + 1
	}
	if h < 1 {
		h = 1
	}
	return h
}
