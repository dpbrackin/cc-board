// Package progress implements `cc-board progress`: a session (usually Claude
// itself) reports how far along its task is, with an optional short note.
//
// The calling session is found from $TMUX_PANE, matched against the pane the
// hook recorded in each status file, then from the Claude Code process that ran
// the command (matched against the PID the hook recorded), and finally from the
// session whose directory contains the current directory. An explicit session
// ID always wins.
package progress

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dpbrackin/cc-board/internal/store"
)

// MaxNote is the longest note kept, in runes; longer notes are cut.
const MaxNote = 80

// Parse reads a progress value: "40", "40%" or "3/5".
func Parse(value string) (store.Progress, error) {
	v := strings.TrimSpace(value)
	if before, after, ok := strings.Cut(v, "/"); ok {
		done, err1 := strconv.Atoi(strings.TrimSpace(before))
		total, err2 := strconv.Atoi(strings.TrimSpace(after))
		if err1 != nil || err2 != nil {
			return store.Progress{}, fmt.Errorf("invalid progress %q: want N, N%% or DONE/TOTAL", value)
		}
		if total <= 0 || done < 0 || done > total {
			return store.Progress{}, fmt.Errorf("invalid progress %q: need 0 <= done <= total and total > 0", value)
		}
		return store.Progress{Percent: done * 100 / total, Done: done, Total: total}, nil
	}
	pct, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
	if err != nil {
		return store.Progress{}, fmt.Errorf("invalid progress %q: want N, N%% or DONE/TOTAL", value)
	}
	if pct < 0 || pct > 100 {
		return store.Progress{}, fmt.Errorf("invalid progress %q: percent must be 0-100", value)
	}
	return store.Progress{Percent: pct}, nil
}

// CleanNote collapses whitespace and cuts the note to MaxNote runes.
func CleanNote(note string) string {
	note = strings.Join(strings.Fields(note), " ")
	if utf8.RuneCountInString(note) > MaxNote {
		r := []rune(note)
		note = string(r[:MaxNote-1]) + "…"
	}
	return note
}

// ErrNoSession means the calling session could not be identified.
var ErrNoSession = errors.New("could not tell which Claude Code session this is; " +
	"run it from inside the session (its terminal or tmux pane), or pass -session <id> " +
	"(the cc-board hook must be installed so sessions are recorded)")

// Resolve finds the session ID for the caller. pane is $TMUX_PANE, pid is the
// Claude Code process the caller runs under, and cwd is the current directory;
// any may be empty or 0. They are tried in that order. Sessions that have
// ended are skipped, and the most recently updated match wins (pane IDs and
// PIDs are reused).
func Resolve(statuses map[string]store.Status, pane string, pid int, cwd string) (string, error) {
	var best store.Status
	found := false
	consider := func(st store.Status) {
		if st.Status == store.StatusEnded {
			return
		}
		if !found || newer(st, best) {
			best, found = st, true
		}
	}
	if pane != "" {
		for _, st := range statuses {
			if st.TmuxPane == pane {
				consider(st)
			}
		}
		if found {
			return best.SessionID, nil
		}
	}
	if pid > 0 {
		for _, st := range statuses {
			if st.PID == pid {
				consider(st)
			}
		}
		if found {
			return best.SessionID, nil
		}
	}
	if cwd != "" {
		// Prefer the deepest matching directory, so a session started in ~
		// doesn't claim every project below it.
		depth := -1
		for _, st := range statuses {
			if st.Status == store.StatusEnded || st.CWD == "" || !within(cwd, st.CWD) {
				continue
			}
			d := len(filepath.Clean(st.CWD))
			if d > depth || (d == depth && newer(st, best)) {
				best, depth, found = st, d, true
			}
		}
		if found {
			return best.SessionID, nil
		}
	}
	return "", ErrNoSession
}

// newer orders candidates: most recently updated first, then by session ID so
// the result never depends on map iteration order.
func newer(a, b store.Status) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.SessionID < b.SessionID
}

// within reports whether dir is base or inside it.
func within(dir, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Report writes progress for the session. now stamps UpdatedAt.
func Report(s *store.Store, id, value, note string, now time.Time) (store.Progress, error) {
	p, err := Parse(value)
	if err != nil {
		return p, err
	}
	p.Note = CleanNote(note)
	p.UpdatedAt = now.UTC()
	return p, s.WriteProgress(id, p)
}
