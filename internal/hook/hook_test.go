package hook

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dpbrackin/cc-board/internal/store"
)

const sid = "11111111-1111-4111-8111-111111111111"

func payload(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runCapturingStdout runs Run with os.Stdout redirected and returns what was
// written to it.
func runCapturingStdout(t *testing.T, boardDir string, in []byte) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	Run(boardDir, bytes.NewReader(in))
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestSequence(t *testing.T) {
	steps := []struct {
		payload string
		want    string
	}{
		{"session_start.json", store.StatusIdle},
		{"notification_idle.json", store.StatusIdle}, // ignored while idle
		{"user_prompt_submit.json", store.StatusWorking},
		{"notification_permission.json", store.StatusNeedsInput},
		{"post_tool_use.json", store.StatusWorking},
		{"unknown_event.json", store.StatusWorking}, // unknown event ignored
		{"stop.json", store.StatusIdle},
		{"session_end.json", store.StatusEnded},
	}
	board := filepath.Join(t.TempDir(), "board")
	t.Setenv("TMUX_PANE", "%12")
	s := store.New(board)
	var started time.Time
	for i, step := range steps {
		out := runCapturingStdout(t, board, payload(t, step.payload))
		if out != "" {
			t.Fatalf("%s: stdout = %q, want empty", step.payload, out)
		}
		st, ok, err := s.ReadStatus(sid)
		if err != nil || !ok {
			t.Fatalf("%s: ReadStatus ok=%v err=%v", step.payload, ok, err)
		}
		if st.Status != step.want {
			t.Errorf("%s: status = %q, want %q", step.payload, st.Status, step.want)
		}
		if st.TmuxPane != "%12" || st.CWD != "/home/dev/projects/api" || !strings.HasSuffix(st.TranscriptPath, sid+".jsonl") {
			t.Errorf("%s: fields = %+v", step.payload, st)
		}
		if i == 0 {
			started = st.StartedAt
			if started.IsZero() || st.LastEvent != "SessionStart" {
				t.Errorf("first event: %+v", st)
			}
		} else if !st.StartedAt.Equal(started) {
			t.Errorf("%s: started_at changed", step.payload)
		}
	}
	if _, err := os.Stat(filepath.Join(board, "hook.log")); err == nil {
		t.Errorf("hook.log written for valid events")
	}
}

func TestNextStatus(t *testing.T) {
	tests := []struct {
		event, cur, want string
		ok               bool
	}{
		{"SessionStart", "", store.StatusIdle, true},
		{"UserPromptSubmit", store.StatusIdle, store.StatusWorking, true},
		{"PostToolUse", store.StatusNeedsInput, store.StatusWorking, true},
		{"Notification", store.StatusWorking, store.StatusNeedsInput, true},
		{"Notification", store.StatusIdle, "", false},
		{"Notification", "", store.StatusNeedsInput, true},
		{"Stop", store.StatusWorking, store.StatusIdle, true},
		{"SessionEnd", store.StatusIdle, store.StatusEnded, true},
		{"PreToolUse", store.StatusIdle, "", false},
	}
	for _, tt := range tests {
		got, ok := NextStatus(tt.event, tt.cur)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NextStatus(%q,%q) = %q,%v want %q,%v", tt.event, tt.cur, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBadInputLogsAndStaysQuiet(t *testing.T) {
	board := filepath.Join(t.TempDir(), "board")
	for _, in := range []string{"not json", `{"hook_event_name":"Stop"}`, `{"session_id":"../x","hook_event_name":"Stop"}`} {
		if out := runCapturingStdout(t, board, []byte(in)); out != "" {
			t.Fatalf("stdout = %q", out)
		}
	}
	log, err := os.ReadFile(filepath.Join(board, "hook.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(log), "\n"); n != 3 {
		t.Errorf("hook.log has %d lines, want 3:\n%s", n, log)
	}
	if entries, _ := os.ReadDir(filepath.Join(board, "sessions")); len(entries) != 0 {
		t.Errorf("status files written for bad input")
	}
}

func TestUnknownFieldsAndOutsideTmux(t *testing.T) {
	board := t.TempDir()
	t.Setenv("TMUX_PANE", "")
	in := `{"session_id":"abc","hook_event_name":"UserPromptSubmit","cwd":"/w","extra":{"nested":[1,2]}}`
	runCapturingStdout(t, board, []byte(in))
	st, ok, _ := store.New(board).ReadStatus("abc")
	if !ok || st.Status != store.StatusWorking || st.TmuxPane != "" || st.StartedAt.IsZero() {
		t.Errorf("got %+v ok=%v", st, ok)
	}
}
