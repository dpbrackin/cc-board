package session

import (
	"reflect"
	"testing"
	"time"

	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/transcript"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func allDirs(string) bool { return true }

func TestMerge(t *testing.T) {
	livePanes := map[string]bool{"%1": true, "%2": true}
	live := func(p string) bool { return livePanes[p] }
	tests := []struct {
		name   string
		info   transcript.Info
		status *store.Status
		meta   store.Meta
		want   func(Session) bool
		desc   string
	}{
		{
			name: "no status file is closed",
			info: transcript.Info{SessionID: "a", FirstPrompt: "Prompt"},
			want: func(s Session) bool { return s.Status == Closed && s.Title == "Prompt" && !s.Live },
		},
		{
			name:   "live working",
			info:   transcript.Info{SessionID: "a"},
			status: &store.Status{SessionID: "a", Status: store.StatusWorking, TmuxPane: "%1"},
			want:   func(s Session) bool { return s.Status == Working && s.Live && s.Title == Untitled },
		},
		{
			name:   "label wins over prompt",
			info:   transcript.Info{SessionID: "a", FirstPrompt: "Prompt"},
			meta:   store.Meta{Label: "Label", Done: true, Hidden: true},
			status: &store.Status{SessionID: "a", Status: store.StatusIdle, TmuxPane: "%1"},
			want:   func(s Session) bool { return s.Title == "Label" && s.Done && s.Hidden && s.Status == Idle },
		},
		{
			name:   "ended is closed even with live pane",
			info:   transcript.Info{SessionID: "a"},
			status: &store.Status{SessionID: "a", Status: store.StatusEnded, TmuxPane: "%1"},
			want:   func(s Session) bool { return s.Status == Closed && !s.Live },
		},
		{
			name:   "pane gone is closed",
			info:   transcript.Info{SessionID: "a"},
			status: &store.Status{SessionID: "a", Status: store.StatusWorking, TmuxPane: "%9"},
			want:   func(s Session) bool { return s.Status == Closed && s.RawStatus == store.StatusWorking },
		},
		{
			name:   "no pane is closed",
			info:   transcript.Info{SessionID: "a"},
			status: &store.Status{SessionID: "a", Status: store.StatusNeedsInput},
			want:   func(s Session) bool { return s.Status == Closed },
		},
		{
			name:   "status updated_at newer than transcript",
			info:   transcript.Info{SessionID: "a", LastActivity: t0},
			status: &store.Status{SessionID: "a", Status: store.StatusIdle, UpdatedAt: t0.Add(time.Minute)},
			want:   func(s Session) bool { return s.UpdatedAt.Equal(t0.Add(time.Minute)) && s.LastActivity.Equal(t0) },
		},
		{
			name:   "progress and done hint",
			info:   transcript.Info{SessionID: "a", TodoDone: 6, TodoTotal: 6},
			status: &store.Status{SessionID: "a", Status: store.StatusIdle, TmuxPane: "%2"},
			want:   func(s Session) bool { return s.HasProgress() && s.DoneHint() },
		},
		{
			name:   "no hint when working",
			info:   transcript.Info{SessionID: "a", TodoDone: 6, TodoTotal: 6},
			status: &store.Status{SessionID: "a", Status: store.StatusWorking, TmuxPane: "%2"},
			want:   func(s Session) bool { return !s.DoneHint() },
		},
		{
			name:   "no hint when marked done",
			info:   transcript.Info{SessionID: "a", TodoDone: 6, TodoTotal: 6},
			meta:   store.Meta{Done: true},
			status: &store.Status{SessionID: "a", Status: store.StatusIdle, TmuxPane: "%2"},
			want:   func(s Session) bool { return !s.DoneHint() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statuses := map[string]store.Status{}
			if tt.status != nil {
				statuses[tt.status.SessionID] = *tt.status
			}
			got := Merge([]transcript.Info{tt.info}, statuses, map[string]store.Meta{tt.info.SessionID: tt.meta}, live, allDirs)
			if len(got) != 1 {
				t.Fatalf("len = %d", len(got))
			}
			if !tt.want(got[0]) {
				t.Errorf("unexpected session: %+v", got[0])
			}
		})
	}
}

func TestStatusWithoutTranscriptIgnored(t *testing.T) {
	got := Merge(nil, map[string]store.Status{"x": {SessionID: "x", Status: store.StatusWorking}}, nil, nil, allDirs)
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestLiveNotCheckedForEnded(t *testing.T) {
	calls := 0
	live := func(string) bool { calls++; return true }
	Merge([]transcript.Info{{SessionID: "a"}}, map[string]store.Status{"a": {Status: store.StatusEnded, TmuxPane: "%1"}}, nil, live, allDirs)
	if calls != 0 {
		t.Errorf("live called %d times for ended session", calls)
	}
}

func TestCWDMissing(t *testing.T) {
	got := Merge([]transcript.Info{{SessionID: "a", CWD: "/definitely/not/here"}}, nil, nil, nil, nil)
	if !got[0].CWDMissing {
		t.Error("expected CWDMissing")
	}
}

func ids(ss []Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func TestGroupAndSort(t *testing.T) {
	m := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }
	sessions := []Session{
		{ID: "web-idle", CWD: "/p/web", Status: Idle, UpdatedAt: m(1)},
		{ID: "api-closed-new", CWD: "/p/api", Status: Closed, UpdatedAt: m(30)},
		{ID: "api-working", CWD: "/p/api", Status: Working, UpdatedAt: m(5)},
		{ID: "api-needs", CWD: "/p/api", Status: NeedsInput, UpdatedAt: m(2)},
		{ID: "api-idle-old", CWD: "/p/api", Status: Idle, UpdatedAt: m(3)},
		{ID: "api-idle-new", CWD: "/p/api", Status: Idle, UpdatedAt: m(4)},
		{ID: "api-done-needs", CWD: "/p/api", Status: NeedsInput, UpdatedAt: m(10), Done: true},
		{ID: "cli-closed", CWD: "/p/cli", Status: Closed, UpdatedAt: m(20)},
	}
	groups := GroupAndSort(sessions)
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	if want := []string{"api", "cli", "web"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("groups = %v, want %v", names, want)
	}
	want := []string{"api-needs", "api-working", "api-idle-new", "api-idle-old", "api-closed-new", "api-done-needs"}
	if got := ids(groups[0].Sessions); !reflect.DeepEqual(got, want) {
		t.Errorf("api order = %v, want %v", got, want)
	}
}

func TestVisible(t *testing.T) {
	now := t0
	sessions := []Session{
		{ID: "hidden", Status: Idle, Hidden: true, UpdatedAt: now},
		{ID: "old-closed", Status: Closed, UpdatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "recent-closed", Status: Closed, UpdatedAt: now.Add(-6 * 24 * time.Hour)},
		{ID: "old-idle", Status: Idle, UpdatedAt: now.Add(-30 * 24 * time.Hour)},
	}
	tests := []struct {
		name    string
		showAll bool
		after   time.Duration
		want    []string
	}{
		{"default", false, 168 * time.Hour, []string{"recent-closed", "old-idle"}},
		{"show all", true, 168 * time.Hour, []string{"hidden", "old-closed", "recent-closed", "old-idle"}},
		{"never hide by age", false, 0, []string{"old-closed", "recent-closed", "old-idle"}},
	}
	for _, tt := range tests {
		if got := ids(Visible(sessions, tt.showAll, tt.after, now)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{2 * time.Minute, "2m ago"},
		{5 * time.Hour, "5h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, tt := range tests {
		if got := RelTime(t0.Add(-tt.d), t0); got != tt.want {
			t.Errorf("RelTime(-%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
	if RelTime(time.Time{}, t0) != "" {
		t.Error("zero time should be blank")
	}
}

func TestCount(t *testing.T) {
	c := Count([]Session{{Status: NeedsInput}, {Status: Working}, {Status: Working}, {Status: Idle}, {Status: Closed}})
	if c != (Counts{1, 2, 1, 1}) {
		t.Errorf("got %+v", c)
	}
}
