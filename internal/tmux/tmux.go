// Package tmux checks pane liveness and switches to or creates tmux windows.
// Every tmux invocation goes through the Runner interface so tests can use a
// fake.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner runs a tmux command and returns its stdout.
type Runner interface {
	Run(args ...string) (string, error)
}

// ExecRunner runs the real tmux binary.
type ExecRunner struct {
	// Bin is the tmux binary; empty means "tmux" from PATH.
	Bin string
}

// Run executes tmux with args.
func (r ExecRunner) Run(args ...string) (string, error) {
	bin := r.Bin
	if bin == "" {
		bin = "tmux"
	}
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("tmux %s: %w: %s", args[0], err, msg)
		}
		return stdout.String(), fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// InTmux reports whether this process runs inside tmux ($TMUX set).
func InTmux() bool { return os.Getenv("TMUX") != "" }

// Errors returned by Jump and Open.
var (
	ErrNotInTmux  = errors.New("not inside tmux: can't jump to a live pane")
	ErrCWDMissing = errors.New("project directory no longer exists")
)

// Client wraps a Runner with cc-board's tmux operations.
type Client struct {
	R Runner
	// LiveCommands are pane_current_command values that count as a running session.
	LiveCommands []string
	// InTmux is true when cc-board itself runs inside tmux.
	InTmux bool
	// ResumeCommand is the resume template; {id} is replaced by the session ID.
	ResumeCommand string
}

// PaneCommand returns the pane's current command.
func (c Client) PaneCommand(pane string) (string, error) {
	out, err := c.R.Run("display-message", "-p", "-t", pane, "#{pane_current_command}")
	return strings.TrimSpace(out), err
}

// IsLive reports whether pane exists and runs one of LiveCommands. Pane IDs
// are reused, so a pane running something else counts as not live.
func (c Client) IsLive(pane string) bool {
	if pane == "" {
		return false
	}
	cmd, err := c.PaneCommand(pane)
	if err != nil || cmd == "" {
		return false
	}
	for _, lc := range c.LiveCommands {
		if cmd == lc {
			return true
		}
	}
	return false
}

// SwitchTo makes pane the active pane of the current client.
func (c Client) SwitchTo(pane string) error {
	_, err := c.R.Run("select-window", "-t", pane, ";", "select-pane", "-t", pane, ";", "switch-client", "-t", pane)
	return err
}

// NewWindow opens a window in dir in the client's current session. An empty
// command opens the default shell; an empty name lets tmux pick one.
func (c Client) NewWindow(dir, name, command string) error {
	args := []string{"new-window", "-c", dir}
	if name != "" {
		args = append(args, "-n", name)
	}
	if command != "" {
		args = append(args, command)
	}
	_, err := c.R.Run(args...)
	return err
}

// Target identifies the session to jump to.
type Target struct {
	ID    string
	CWD   string
	Pane  string
	Title string
}

// Outcome says what Jump or Open did.
type Outcome int

const (
	// Switched to a live pane.
	Switched Outcome = iota
	// NewWindow opened a tmux window.
	NewWindow
	// Exec means the caller must exit and exec the command itself (outside tmux).
	Exec
)

// ExecSpec is a command to exec after the TUI exits (outside tmux).
type ExecSpec struct {
	Dir  string
	Argv []string
}

// Jump switches to the session's live pane, or resumes it in a new window.
// Outside tmux it returns Exec with the resume command for the caller to run.
func (c Client) Jump(t Target) (Outcome, *ExecSpec, error) {
	if t.Pane != "" && c.IsLive(t.Pane) {
		if !c.InTmux {
			return 0, nil, ErrNotInTmux
		}
		return Switched, nil, c.SwitchTo(t.Pane)
	}
	if !dirExists(t.CWD) {
		return 0, nil, ErrCWDMissing
	}
	resume := ResumeCommand(c.ResumeCommand, t.ID)
	if !c.InTmux {
		return Exec, &ExecSpec{Dir: t.CWD, Argv: []string{Shell(), "-c", resume}}, nil
	}
	return NewWindow, nil, c.NewWindow(t.CWD, WindowName(t.Title), resume)
}

// Open opens a plain shell window in dir, or returns Exec outside tmux.
func (c Client) Open(dir string) (Outcome, *ExecSpec, error) {
	if !dirExists(dir) {
		return 0, nil, ErrCWDMissing
	}
	if !c.InTmux {
		return Exec, &ExecSpec{Dir: dir, Argv: []string{Shell()}}, nil
	}
	return NewWindow, nil, c.NewWindow(dir, "", "")
}

// ResumeCommand fills {id} in tmpl with a shell-quoted session ID.
func ResumeCommand(tmpl, id string) string {
	if tmpl == "" {
		tmpl = "claude --resume {id}"
	}
	return strings.ReplaceAll(tmpl, "{id}", shellQuote(id))
}

// WindowName shortens a title to at most 20 characters for use as a tmux
// window name. It adds no ellipsis: tmux shows non-ASCII as "_" in some locales.
func WindowName(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	r := []rune(title)
	if len(r) > 20 {
		return strings.TrimSpace(string(r[:20]))
	}
	return title
}

// Shell returns $SHELL or /bin/sh.
func Shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dirExists(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
