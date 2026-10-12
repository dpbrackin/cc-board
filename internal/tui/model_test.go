package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dpbrackin/cc-board/internal/session"
	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/tmux"
)

var now = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

type fixture struct {
	m      Model
	runner *tmux.FakeRunner
	store  *store.Store
	dir    string
}

func newFixture(t *testing.T, inTmux bool) *fixture {
	t.Helper()
	dir := t.TempDir()
	api := filepath.Join(dir, "api")
	web := filepath.Join(dir, "web")
	os.Mkdir(api, 0o755)
	os.Mkdir(web, 0o755)
	runner := &tmux.FakeRunner{Panes: map[string]string{"%1": "claude"}}
	st := store.New(filepath.Join(dir, "board"))
	m := New(Options{
		Store:           st,
		Client:          tmux.Client{R: runner, LiveCommands: []string{"claude"}, InTmux: inTmux, ResumeCommand: "claude --resume {id}"},
		HideClosedAfter: 168 * time.Hour,
		Now:             func() time.Time { return now },
	})
	sessions := []session.Session{
		{ID: "needs", Title: "Add rate limiting", CWD: api, Status: session.NeedsInput, Pane: "%1", Live: true, UpdatedAt: now, LastAssistant: "May I run the tests?"},
		{ID: "work", Title: "Fix flaky tests", CWD: api, Status: session.Working, UpdatedAt: now.Add(-2 * time.Minute), TodoDone: 1, TodoTotal: 4},
		{ID: "idle", Title: "Migrate settings", FirstPrompt: "Migrate settings", CWD: web, Status: session.Idle, UpdatedAt: now.Add(-18 * time.Minute), TodoDone: 6, TodoTotal: 6},
		{ID: "closed", Title: "Upgrade React", CWD: web, Status: session.Closed, UpdatedAt: now.Add(-48 * time.Hour)},
		{ID: "old", Title: "Ancient", CWD: web, Status: session.Closed, UpdatedAt: now.Add(-30 * 24 * time.Hour)},
		{ID: "gone", Title: "Deleted project", CWD: filepath.Join(dir, "deleted"), Status: session.Closed, UpdatedAt: now.Add(-time.Hour), CWDMissing: true},
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ = next.Update(loadedMsg{sessions: sessions})
	return &fixture{m: next.(Model), runner: runner, store: st, dir: dir}
}

// send applies a key and runs any resulting command synchronously, feeding
// its message back into Update (one level deep, which is all the model needs).
func (f *fixture) send(t *testing.T, k string) tea.Cmd {
	t.Helper()
	next, cmd := f.m.Update(key(k))
	f.m = next.(Model)
	return cmd
}

func (f *fixture) run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		return msg
	}
	next, _ := f.m.Update(msg)
	f.m = next.(Model)
	return msg
}

func (f *fixture) currentID() string {
	s, _ := f.m.current()
	return s.ID
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestInitialOrderAndHiding(t *testing.T) {
	f := newFixture(t, true)
	var got []string
	for _, r := range f.m.rows {
		if r.header {
			got = append(got, "#"+r.group.Name)
		} else {
			got = append(got, r.session.ID)
		}
	}
	want := []string{"#api", "needs", "work", "#web", "idle", "closed", "#deleted", "gone"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if f.currentID() != "needs" {
		t.Errorf("cursor on %q", f.currentID())
	}
}

func TestMovementSkipsHeaders(t *testing.T) {
	f := newFixture(t, true)
	var seen []string
	for _, k := range []string{"j", "j", "down", "j", "j", "k", "up"} {
		f.send(t, k)
		seen = append(seen, f.currentID())
	}
	want := []string{"work", "idle", "closed", "gone", "gone", "closed", "idle"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("cursor path = %v, want %v", seen, want)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		f := newFixture(t, true)
		if !isQuit(f.send(t, k)) {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestEnterLivePaneSwitchesAndQuits(t *testing.T) {
	f := newFixture(t, true)
	msg := f.run(f.send(t, "enter"))
	if jm, ok := msg.(jumpMsg); !ok || jm.err != nil || jm.outcome != tmux.Switched {
		t.Fatalf("msg = %#v", msg)
	}
	want := []string{"display-message -p -t %1 #{pane_current_command}", "select-window -t %1 ; select-pane -t %1 ; switch-client -t %1"}
	if got := f.runner.Commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q", got)
	}
	_, cmd := f.m.Update(msg)
	if !isQuit(cmd) {
		t.Error("expected quit after jump")
	}
}

func TestEnterClosedResumesInNewWindow(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "j") // work: no pane
	f.run(f.send(t, "enter"))
	want := []string{"new-window -c " + filepath.Join(f.dir, "api") + " -n Fix flaky tests claude --resume work"}
	if got := f.runner.Commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestEnterMissingCWDShowsError(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "G") // gone (last row)
	msg := f.run(f.send(t, "enter"))
	if jm, ok := msg.(jumpMsg); !ok || jm.err == nil {
		t.Fatalf("msg = %#v", msg)
	}
	if !strings.Contains(f.m.View(), "project directory no longer exists") {
		t.Error("error not shown in footer")
	}
	if len(f.runner.Calls) != 0 {
		t.Errorf("tmux called: %q", f.runner.Commands())
	}
	// "o" on the same row also errors.
	f.run(f.send(t, "o"))
	if !f.m.messageErr {
		t.Error("o did not error")
	}
}

func TestOpenOutsideTmuxExecs(t *testing.T) {
	f := newFixture(t, false)
	t.Setenv("SHELL", "/bin/sh")
	msg := f.run(f.send(t, "o"))
	jm := msg.(jumpMsg)
	if jm.outcome != tmux.Exec || f.m.Exec() == nil || f.m.Exec().Dir != filepath.Join(f.dir, "api") {
		t.Fatalf("exec = %+v", f.m.Exec())
	}
	if !strings.Contains(f.m.View(), "not in tmux") {
		t.Error("footer should say not in tmux")
	}
}

func TestRenameSavesLabel(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "j")
	f.send(t, "j") // idle
	f.send(t, "r")
	if f.m.mode != modeRename {
		t.Fatal("not in rename mode")
	}
	// Clear the prefilled title.
	for range "Migrate settings" {
		f.send(t, "backspace")
	}
	for _, r := range "New name" {
		f.send(t, string(r))
	}
	f.run(f.send(t, "enter"))
	meta, _ := f.store.ReadMeta("idle")
	if meta.Label != "New name" {
		t.Errorf("label = %q", meta.Label)
	}
	if s, _ := f.m.current(); s.Title != "New name" {
		t.Errorf("title = %q", s.Title)
	}
}

func TestRenameEscCancels(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "r")
	f.send(t, "z")
	if cmd := f.send(t, "esc"); isQuit(cmd) {
		t.Fatal("esc in rename quit the app")
	}
	if f.m.mode != modeNormal {
		t.Error("still renaming")
	}
	if meta, _ := f.store.ReadMeta("needs"); meta.Label != "" {
		t.Error("label saved on cancel")
	}
}

func TestToggleDoneAndHidden(t *testing.T) {
	f := newFixture(t, true)
	f.run(f.send(t, "d"))
	meta, _ := f.store.ReadMeta("needs")
	if !meta.Done {
		t.Fatal("done not saved")
	}
	// Done sorts to the bottom of the group; cursor follows the session.
	if f.currentID() != "needs" || f.m.rows[2].session.ID != "needs" {
		t.Errorf("cursor %q, rows[2] %q", f.currentID(), f.m.rows[2].session.ID)
	}
	f.run(f.send(t, "d"))
	if meta, _ := f.store.ReadMeta("needs"); meta.Done {
		t.Error("done not toggled off")
	}

	f.run(f.send(t, "x"))
	if meta, _ := f.store.ReadMeta("needs"); !meta.Hidden {
		t.Fatal("hidden not saved")
	}
	for _, r := range f.m.rows {
		if r.session.ID == "needs" {
			t.Error("hidden session still shown")
		}
	}
}

func TestShowAll(t *testing.T) {
	f := newFixture(t, true)
	count := func() int {
		n := 0
		for _, r := range f.m.rows {
			if !r.header {
				n++
			}
		}
		return n
	}
	if count() != 5 {
		t.Fatalf("default count = %d", count())
	}
	f.send(t, "a")
	if count() != 6 {
		t.Errorf("show all count = %d", count())
	}
	f.send(t, "a")
	if count() != 5 {
		t.Errorf("toggle back count = %d", count())
	}
}

func TestFilter(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "/")
	for _, r := range "web" {
		f.send(t, string(r))
	}
	var ids []string
	for _, r := range f.m.rows {
		if !r.header {
			ids = append(ids, r.session.ID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"idle", "closed"}) {
		t.Errorf("filtered by project = %v", ids)
	}
	f.send(t, "enter")
	if f.m.mode != modeNormal || f.m.filter != "web" {
		t.Fatalf("mode %v filter %q", f.m.mode, f.m.filter)
	}
	// q in normal mode still quits; esc first clears the filter.
	if isQuit(f.send(t, "esc")) || f.m.filter != "" {
		t.Error("esc should clear the filter, not quit")
	}
	f.send(t, "/")
	for _, r := range "flaky" {
		f.send(t, string(r))
	}
	if f.currentID() != "work" {
		t.Errorf("filtered by title: cursor %q", f.currentID())
	}
}

func TestMultiRuneKeyMsgIsSplit(t *testing.T) {
	f := newFixture(t, true)
	next, _ := f.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj/web")})
	f.m = next.(Model)
	if f.m.mode != modeFilter || f.m.filter != "web" {
		t.Errorf("mode %v filter %q", f.m.mode, f.m.filter)
	}
}

func TestPreviewToggle(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "p")
	if !f.m.preview || !strings.Contains(f.m.View(), "May I run the tests?") {
		t.Fatal("preview not shown")
	}
	f.send(t, "j")
	if !strings.Contains(f.m.View(), "(no assistant message yet)") {
		t.Error("preview did not follow cursor")
	}
	f.send(t, "p")
	if f.m.preview {
		t.Error("preview not toggled off")
	}
}

func TestTickSchedulesLoadAndCursorSurvivesRefresh(t *testing.T) {
	f := newFixture(t, true)
	f.send(t, "j") // work
	_, cmd := f.m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("tick did not schedule a load")
	}
	// A refresh that reorders sessions keeps the cursor on the same session.
	ss := append([]session.Session(nil), f.m.sessions...)
	ss[1].Status = session.NeedsInput
	ss[1].UpdatedAt = now.Add(time.Second)
	next, cmd := f.m.Update(loadedMsg{sessions: ss, fromTick: true})
	f.m = next.(Model)
	if cmd == nil {
		t.Error("loaded-from-tick did not schedule the next tick")
	}
	if f.currentID() != "work" || f.m.cursor != 1 {
		t.Errorf("cursor %d on %q", f.m.cursor, f.currentID())
	}
}

func TestViewRenders(t *testing.T) {
	f := newFixture(t, true)
	v := f.m.View()
	for _, want := range []string{"cc-board", "1 needs you", "1 working", "1 idle", "2 closed", "Add rate limiting", "1/4", "6/6", "done?", "2d ago", "enter jump"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if n := strings.Count(v, "\n") + 1; n != 30 {
		t.Errorf("view has %d lines, want 30", n)
	}
}
