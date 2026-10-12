package tmux

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJump(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone")
	t.Setenv("SHELL", "/bin/zsh")
	tests := []struct {
		name     string
		inTmux   bool
		panes    map[string]string
		target   Target
		want     []string
		outcome  Outcome
		exec     *ExecSpec
		wantErr  error
		liveOnly bool
	}{
		{
			name:    "live pane",
			inTmux:  true,
			panes:   map[string]string{"%12": "claude"},
			target:  Target{ID: "abc", CWD: dir, Pane: "%12", Title: "Fix tests"},
			want:    []string{"display-message -p -t %12 #{pane_current_command}", "select-window -t %12 ; select-pane -t %12 ; switch-client -t %12"},
			outcome: Switched,
		},
		{
			name:    "pane reused by another program",
			inTmux:  true,
			panes:   map[string]string{"%12": "bash"},
			target:  Target{ID: "abc", CWD: dir, Pane: "%12", Title: "Fix tests"},
			want:    []string{"display-message -p -t %12 #{pane_current_command}", "new-window -c " + dir + " -n Fix tests claude --resume abc"},
			outcome: NewWindow,
		},
		{
			name:    "closed: pane gone",
			inTmux:  true,
			target:  Target{ID: "abc", CWD: dir, Pane: "%3", Title: "A very long title that needs shortening"},
			want:    []string{"display-message -p -t %3 #{pane_current_command}", "new-window -c " + dir + " -n A very long title t… claude --resume abc"},
			outcome: NewWindow,
		},
		{
			name:    "closed: no pane recorded",
			inTmux:  true,
			target:  Target{ID: "abc", CWD: dir, Title: "t"},
			want:    []string{"new-window -c " + dir + " -n t claude --resume abc"},
			outcome: NewWindow,
		},
		{
			name:    "missing cwd",
			inTmux:  true,
			target:  Target{ID: "abc", CWD: missing, Pane: "%3"},
			want:    []string{"display-message -p -t %3 #{pane_current_command}"},
			wantErr: ErrCWDMissing,
		},
		{
			name:    "live outside tmux",
			panes:   map[string]string{"%12": "node"},
			target:  Target{ID: "abc", CWD: dir, Pane: "%12"},
			want:    []string{"display-message -p -t %12 #{pane_current_command}"},
			wantErr: ErrNotInTmux,
		},
		{
			name:    "closed outside tmux execs resume",
			target:  Target{ID: "abc", CWD: dir},
			want:    nil,
			outcome: Exec,
			exec:    &ExecSpec{Dir: dir, Argv: []string{"/bin/zsh", "-c", "claude --resume abc"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FakeRunner{Panes: tt.panes}
			c := Client{R: f, LiveCommands: []string{"claude", "node"}, InTmux: tt.inTmux, ResumeCommand: "claude --resume {id}"}
			out, ex, err := c.Jump(tt.target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got := f.Commands(); !reflect.DeepEqual(got, tt.want) && !(len(got) == 0 && len(tt.want) == 0) {
				t.Errorf("commands:\n got %q\nwant %q", got, tt.want)
			}
			if err == nil && out != tt.outcome {
				t.Errorf("outcome = %v, want %v", out, tt.outcome)
			}
			if !reflect.DeepEqual(ex, tt.exec) {
				t.Errorf("exec = %+v, want %+v", ex, tt.exec)
			}
		})
	}
}

func TestNewWindowArgsAreSeparate(t *testing.T) {
	// The resume command is passed as one argument; tmux hands it to the shell.
	f := &FakeRunner{}
	c := Client{R: f, InTmux: true, ResumeCommand: "claude --resume {id}"}
	if _, _, err := c.Jump(Target{ID: "abc", CWD: t.TempDir(), Title: "x"}); err != nil {
		t.Fatal(err)
	}
	last := f.Calls[len(f.Calls)-1]
	if last[len(last)-1] != "claude --resume abc" {
		t.Errorf("last arg = %q", last[len(last)-1])
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	f := &FakeRunner{}
	c := Client{R: f, InTmux: true}
	if out, _, err := c.Open(dir); err != nil || out != NewWindow {
		t.Fatalf("Open: %v %v", out, err)
	}
	if got := f.Commands(); !reflect.DeepEqual(got, []string{"new-window -c " + dir}) {
		t.Errorf("commands = %q", got)
	}
	if _, _, err := c.Open(filepath.Join(dir, "nope")); !errors.Is(err, ErrCWDMissing) {
		t.Errorf("err = %v", err)
	}
	t.Setenv("SHELL", "/bin/fish")
	c.InTmux = false
	out, ex, err := c.Open(dir)
	if err != nil || out != Exec || !reflect.DeepEqual(ex, &ExecSpec{Dir: dir, Argv: []string{"/bin/fish"}}) {
		t.Errorf("outside tmux: %v %+v %v", out, ex, err)
	}
}

func TestIsLive(t *testing.T) {
	f := &FakeRunner{Panes: map[string]string{"%1": "claude", "%2": "vim"}}
	c := Client{R: f, LiveCommands: []string{"claude", "node"}}
	if !c.IsLive("%1") || c.IsLive("%2") || c.IsLive("%9") || c.IsLive("") {
		t.Error("unexpected liveness")
	}
}

func TestResumeCommandQuotes(t *testing.T) {
	if got := ResumeCommand("claude -r {id}", "a b'c"); got != `claude -r 'a b'\''c'` {
		t.Errorf("got %q", got)
	}
}
