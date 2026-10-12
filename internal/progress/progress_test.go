package progress

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dpbrackin/cc-board/internal/store"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    store.Progress
		wantErr bool
	}{
		{in: "40", want: store.Progress{Percent: 40}},
		{in: "40%", want: store.Progress{Percent: 40}},
		{in: " 0 ", want: store.Progress{Percent: 0}},
		{in: "100", want: store.Progress{Percent: 100}},
		{in: "3/5", want: store.Progress{Percent: 60, Done: 3, Total: 5}},
		{in: "1/3", want: store.Progress{Percent: 33, Done: 1, Total: 3}},
		{in: "5/5", want: store.Progress{Percent: 100, Done: 5, Total: 5}},
		{in: "101", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "6/5", wantErr: true},
		{in: "1/0", wantErr: true},
		{in: "x/5", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("Parse(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestCleanNote(t *testing.T) {
	if got := CleanNote("  writing\n  tests  "); got != "writing tests" {
		t.Errorf("got %q", got)
	}
	long := CleanNote(strings.Repeat("é", 200))
	if n := len([]rune(long)); n != MaxNote || !strings.HasSuffix(long, "…") {
		t.Errorf("long note: %d runes, %q", n, long[len(long)-6:])
	}
}

func TestResolve(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	statuses := map[string]store.Status{
		"old-pane": {SessionID: "old-pane", TmuxPane: "%3", CWD: "/w/api", Status: store.StatusIdle, UpdatedAt: t1},
		"new-pane": {SessionID: "new-pane", TmuxPane: "%3", CWD: "/w/api", Status: store.StatusWorking, UpdatedAt: t2},
		"ended":    {SessionID: "ended", TmuxPane: "%4", CWD: "/w/web", Status: store.StatusEnded, UpdatedAt: t2},
		"home":     {SessionID: "home", TmuxPane: "%5", CWD: "/w", Status: store.StatusIdle, UpdatedAt: t2.Add(time.Hour)},
		"web":      {SessionID: "web", TmuxPane: "%6", CWD: "/w/web", Status: store.StatusIdle, UpdatedAt: t1},
	}
	tests := []struct {
		name, pane, cwd, want string
		wantErr               bool
	}{
		{name: "pane match, most recent wins", pane: "%3", cwd: "/elsewhere", want: "new-pane"},
		{name: "ended pane falls back to cwd", pane: "%4", cwd: "/w/web/src", want: "web"},
		{name: "deepest cwd beats more recent parent", cwd: "/w/web/src", want: "web"},
		{name: "parent dir only", cwd: "/w/other", want: "home"},
		{name: "unknown pane and cwd", pane: "%9", cwd: "/tmp", wantErr: true},
		{name: "sibling prefix is not inside", cwd: "/w-other", wantErr: true},
		{name: "nothing to go on", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(statuses, tt.pane, tt.cwd)
			if tt.wantErr {
				if !errors.Is(err, ErrNoSession) {
					t.Fatalf("got %q, %v; want ErrNoSession", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestReport(t *testing.T) {
	s := store.New(filepath.Join(t.TempDir(), "board"))
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.FixedZone("PHT", 8*3600))
	if _, err := Report(s, "abc", "2/4", "wiring  up the API", now); err != nil {
		t.Fatal(err)
	}
	all, err := s.ReadAllProgress()
	if err != nil {
		t.Fatal(err)
	}
	want := store.Progress{Percent: 50, Done: 2, Total: 4, Note: "wiring up the API", UpdatedAt: now.UTC()}
	if got := all["abc"]; got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := Report(s, "abc", "nope", "", now); err == nil {
		t.Error("bad value: want error")
	}
	if err := s.DeleteProgress("abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProgress("abc"); err != nil {
		t.Errorf("deleting a missing file: %v", err)
	}
	if all, _ := s.ReadAllProgress(); len(all) != 0 {
		t.Errorf("after delete: %+v", all)
	}
}
