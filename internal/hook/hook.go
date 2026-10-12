// Package hook implements `cc-board hook`: it reads a Claude Code hook event
// from stdin and updates the session's status file.
//
// The hook must never write to stdout (output of some hooks is added to
// Claude's context), must always exit 0, and must be fast: no tmux calls and
// no transcript parsing.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dpbrackin/cc-board/internal/proc"
	"github.com/dpbrackin/cc-board/internal/store"
)

// Event is the subset of the hook stdin payload that cc-board reads.
type Event struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// maxInput bounds how much of stdin is read (PostToolUse payloads can carry
// large tool responses).
const maxInput = 64 << 20

// NextStatus maps an event to the new status given the current one. ok is
// false for unknown events and for events that should not change the file.
func NextStatus(event, current string) (status string, ok bool) {
	switch event {
	case "SessionStart":
		return store.StatusIdle, true
	case "UserPromptSubmit", "PostToolUse":
		return store.StatusWorking, true
	case "Notification":
		// The idle reminder fires after a finished turn; don't flip it.
		if current == store.StatusIdle {
			return "", false
		}
		return store.StatusNeedsInput, true
	case "Stop":
		return store.StatusIdle, true
	case "SessionEnd":
		return store.StatusEnded, true
	}
	return "", false
}

// Handler applies events to a store.
type Handler struct {
	Store *store.Store
	Now   func() time.Time
	// TmuxPane is the value of $TMUX_PANE for this process.
	TmuxPane string
	// ClaudePID finds the Claude Code process that ran the hook. It is called
	// only on SessionStart, or when no PID is recorded yet, to keep normal
	// events cheap. It returns 0 when the process can't be found.
	ClaudePID func() int
}

// Handle reads one event from r and updates the status file.
func (h *Handler) Handle(r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, maxInput))
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	if ev.SessionID == "" {
		return fmt.Errorf("event %q without session_id", ev.HookEventName)
	}
	if !store.ValidID(ev.SessionID) {
		return fmt.Errorf("invalid session_id %q", ev.SessionID)
	}
	cur, exists, err := h.Store.ReadStatus(ev.SessionID)
	if err != nil {
		// A corrupt file is rewritten from scratch.
		cur, exists = store.Status{}, false
	}
	status, ok := NextStatus(ev.HookEventName, cur.Status)
	if !ok {
		return nil
	}
	now := h.now()
	st := cur
	st.SessionID = ev.SessionID
	if ev.CWD != "" {
		st.CWD = ev.CWD
	}
	if ev.TranscriptPath != "" {
		st.TranscriptPath = ev.TranscriptPath
	}
	if h.TmuxPane != "" || ev.HookEventName == "SessionStart" {
		st.TmuxPane = h.TmuxPane
	}
	if (ev.HookEventName == "SessionStart" || st.PID == 0) && h.ClaudePID != nil {
		if pid := h.ClaudePID(); pid > 0 || ev.HookEventName == "SessionStart" {
			st.PID = pid
		}
	}
	st.Status = status
	st.LastEvent = ev.HookEventName
	if !exists || st.StartedAt.IsZero() || ev.HookEventName == "SessionStart" && cur.Status == store.StatusEnded {
		st.StartedAt = now
	}
	st.UpdatedAt = now
	return h.Store.WriteStatus(st)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

// Run is the entry point for `cc-board hook`. It never writes to stdout and
// always returns normally; errors are appended to <board>/hook.log.
// liveCommands are the process names that count as Claude Code.
func Run(boardDir string, liveCommands []string, stdin io.Reader) {
	defer func() {
		if r := recover(); r != nil {
			logError(boardDir, fmt.Errorf("panic: %v", r))
		}
	}()
	h := &Handler{
		Store:    store.New(boardDir),
		TmuxPane: os.Getenv("TMUX_PANE"),
		ClaudePID: func() int {
			info, _ := proc.FindAncestor(nil, os.Getppid(), liveCommands)
			return info.PID
		},
	}
	if err := h.Handle(stdin); err != nil {
		logError(boardDir, err)
	}
}

func logError(boardDir string, err error) {
	if mkErr := os.MkdirAll(boardDir, 0o755); mkErr != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(boardDir, "hook.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().UTC().Format(time.RFC3339), err)
}
