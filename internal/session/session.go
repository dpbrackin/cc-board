// Package session merges transcripts, status files and meta files into the
// Session rows shown by the TUI and `cc-board list`, and sorts and groups them.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/transcript"
)

// Display statuses. needs_input, working and idle come from the status file;
// closed covers ended sessions, sessions without a live pane, and sessions
// with no status file.
const (
	NeedsInput = store.StatusNeedsInput
	Working    = store.StatusWorking
	Idle       = store.StatusIdle
	Closed     = "closed"
)

// Untitled is the title of a session with no label and no prompt.
const Untitled = "(untitled)"

// Session is one row of the board.
type Session struct {
	ID             string
	Title          string
	Label          string // meta label
	FirstPrompt    string
	CWD            string
	Status         string // display status: NeedsInput, Working, Idle or Closed
	RawStatus      string // status file value, empty if none
	Pane           string
	Live           bool
	TodoDone       int
	TodoTotal      int
	LastActivity   time.Time // transcript last timestamp (or mtime)
	UpdatedAt      time.Time // max(status updated_at, LastActivity), used for sorting
	Done           bool
	Hidden         bool
	CWDMissing     bool
	LastAssistant  string
	TranscriptPath string
}

// Project is the group name: the basename of the cwd.
func (s Session) Project() string {
	if s.CWD == "" {
		return "(unknown)"
	}
	return filepath.Base(s.CWD)
}

// HasProgress reports whether the session has a todo list.
func (s Session) HasProgress() bool { return s.TodoTotal > 0 }

// DoneHint reports whether to show the dim "done?" hint: every todo is
// complete, the session is idle, and the user hasn't marked it done.
func (s Session) DoneHint() bool {
	return !s.Done && s.Status == Idle && s.TodoTotal > 0 && s.TodoDone == s.TodoTotal
}

// LiveFunc reports whether a tmux pane is live (exists and runs Claude).
type LiveFunc func(pane string) bool

// DirExistsFunc reports whether a directory exists.
type DirExistsFunc func(dir string) bool

// Merge builds sessions from transcripts, status and meta files.
//
//  1. Every transcript is a session; status files without one are ignored.
//  2. Title = meta label, else first prompt, else "(untitled)".
//  3. Status comes from the status file; none means closed.
//  4. Live only if status is not ended, a pane is set, and live(pane) is true;
//     otherwise the session displays as closed.
//  5. Progress = completed/total of the latest todo list.
//  6. Done = meta done.
func Merge(infos []transcript.Info, statuses map[string]store.Status, metas map[string]store.Meta, live LiveFunc, dirExists DirExistsFunc) []Session {
	if dirExists == nil {
		dirExists = DirExists
	}
	out := make([]Session, 0, len(infos))
	for _, in := range infos {
		st, hasStatus := statuses[in.SessionID]
		meta := metas[in.SessionID]
		s := Session{
			ID:             in.SessionID,
			Label:          meta.Label,
			FirstPrompt:    in.FirstPrompt,
			CWD:            in.CWD,
			TodoDone:       in.TodoDone,
			TodoTotal:      in.TodoTotal,
			LastActivity:   in.LastActivity,
			UpdatedAt:      in.LastActivity,
			Done:           meta.Done,
			Hidden:         meta.Hidden,
			LastAssistant:  in.LastAssistant,
			TranscriptPath: in.Path,
			Status:         Closed,
		}
		switch {
		case meta.Label != "":
			s.Title = meta.Label
		case in.FirstPrompt != "":
			s.Title = in.FirstPrompt
		default:
			s.Title = Untitled
		}
		if hasStatus {
			s.RawStatus = st.Status
			s.Pane = st.TmuxPane
			if s.CWD == "" {
				s.CWD = st.CWD
			}
			if st.UpdatedAt.After(s.UpdatedAt) {
				s.UpdatedAt = st.UpdatedAt
			}
			if st.Status != store.StatusEnded && st.TmuxPane != "" && live != nil && live(st.TmuxPane) {
				s.Live = true
				switch st.Status {
				case NeedsInput, Working, Idle:
					s.Status = st.Status
				default:
					s.Status = Idle
				}
			}
		}
		s.CWDMissing = s.CWD == "" || !dirExists(s.CWD)
		out = append(out, s)
	}
	return out
}

// DirExists reports whether dir is an existing directory.
func DirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

var statusRank = map[string]int{NeedsInput: 0, Working: 1, Idle: 2, Closed: 3}

// Less orders sessions within a group: not-done before done, then
// needs_input, working, idle, closed, then most recently updated first.
func Less(a, b Session) bool {
	if a.Done != b.Done {
		return !a.Done
	}
	if ra, rb := statusRank[a.Status], statusRank[b.Status]; ra != rb {
		return ra < rb
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID < b.ID
}

// Group is a project directory and its sessions.
type Group struct {
	Dir      string
	Name     string
	Latest   time.Time
	Sessions []Session
}

// GroupAndSort groups sessions by cwd, sorts groups by most recent activity
// and sorts sessions within each group with Less.
func GroupAndSort(sessions []Session) []Group {
	idx := map[string]int{}
	var groups []Group
	for _, s := range sessions {
		i, ok := idx[s.CWD]
		if !ok {
			i = len(groups)
			idx[s.CWD] = i
			groups = append(groups, Group{Dir: s.CWD, Name: s.Project()})
		}
		g := &groups[i]
		g.Sessions = append(g.Sessions, s)
		if s.UpdatedAt.After(g.Latest) {
			g.Latest = s.UpdatedAt
		}
	}
	for i := range groups {
		ss := groups[i].Sessions
		sort.SliceStable(ss, func(a, b int) bool { return Less(ss[a], ss[b]) })
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if !groups[a].Latest.Equal(groups[b].Latest) {
			return groups[a].Latest.After(groups[b].Latest)
		}
		return groups[a].Dir < groups[b].Dir
	})
	return groups
}

// Visible filters sessions for the default view: it drops hidden sessions and
// sessions closed for longer than hideClosedAfter. showAll keeps everything.
// A hideClosedAfter of 0 never hides closed sessions by age.
func Visible(sessions []Session, showAll bool, hideClosedAfter time.Duration, now time.Time) []Session {
	if showAll {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Hidden {
			continue
		}
		if s.Status == Closed && hideClosedAfter > 0 && now.Sub(s.UpdatedAt) > hideClosedAfter {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Counts tallies display statuses.
type Counts struct {
	NeedsInput, Working, Idle, Closed int
}

// Count tallies the statuses of sessions.
func Count(sessions []Session) Counts {
	var c Counts
	for _, s := range sessions {
		switch s.Status {
		case NeedsInput:
			c.NeedsInput++
		case Working:
			c.Working++
		case Idle:
			c.Idle++
		default:
			c.Closed++
		}
	}
	return c
}

// RelTime formats t relative to now: "just now", "5m ago", "3h ago", "2d ago".
func RelTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

// ShortenHome replaces the home directory prefix of p with "~".
func ShortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// Loader reads all sources and merges them. Transcripts are cached between
// calls, so Load is cheap when nothing changed.
type Loader struct {
	ProjectsDir string
	Store       *store.Store
	Cache       *transcript.Cache
	Live        LiveFunc
}

// Load returns the merged (unsorted, unfiltered) sessions.
func (l *Loader) Load() ([]Session, error) {
	if l.Cache == nil {
		l.Cache = transcript.NewCache()
	}
	infos, err := l.Cache.Scan(l.ProjectsDir)
	if err != nil {
		return nil, err
	}
	statuses, err := l.Store.ReadAllStatus()
	if err != nil {
		return nil, err
	}
	metas, err := l.Store.ReadAllMeta()
	if err != nil {
		return nil, err
	}
	return Merge(infos, statuses, metas, l.Live, nil), nil
}
