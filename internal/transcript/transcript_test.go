package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(name string) string {
	return filepath.Join("..", "..", "testdata", "transcripts", name)
}

func TestParseFixtures(t *testing.T) {
	tests := []struct {
		file          string
		sessionID     string
		cwd           string
		firstPrompt   string
		done, total   int
		lastAssistant string
		lastActivity  string
	}{
		{
			file:          "no_todos.jsonl",
			sessionID:     "11111111-1111-4111-8111-111111111111",
			cwd:           "/home/dev/projects/api",
			firstPrompt:   "Add rate limiting to the /login endpoint",
			lastAssistant: "I added a token bucket limiter to /login.",
			lastActivity:  "2026-03-01T10:00:07Z",
		},
		{
			file:          "todowrite.jsonl",
			sessionID:     "22222222-2222-4222-8222-222222222222",
			cwd:           "/home/dev/projects/web",
			firstPrompt:   "Migrate the settings page to the new forms library and make…",
			done:          3,
			total:         4,
			lastAssistant: "Settings page migrated; one step left.",
		},
		{
			file:          "tasks.jsonl",
			sessionID:     "33333333-3333-4333-8333-333333333333",
			cwd:           "/home/dev/projects/api",
			firstPrompt:   "Fix the flaky integration tests",
			done:          1,
			total:         3, // 4 created, 1 deleted
			lastAssistant: "First test fixed.",
		},
		{
			file:          "malformed.jsonl",
			sessionID:     "44444444-4444-4444-8444-444444444444",
			cwd:           "/home/dev/projects/cli",
			firstPrompt:   "Upgrade to React 19",
			lastAssistant: "Upgraded.",
		},
		{
			file:          "meta_first.jsonl",
			sessionID:     "55555555-5555-4555-8555-555555555555",
			cwd:           "/home/dev/projects/web",
			firstPrompt:   "Write release notes for v2",
			lastAssistant: "Here are the notes.",
		},
		{
			file:      "meta_only.jsonl",
			sessionID: "66666666-6666-4666-8666-666666666666",
			cwd:       "/home/dev/projects/web",
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			f, err := os.Open(fixture(tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			info, err := Parse(f)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if info.SessionID != tt.sessionID {
				t.Errorf("SessionID = %q, want %q", info.SessionID, tt.sessionID)
			}
			if info.CWD != tt.cwd {
				t.Errorf("CWD = %q, want %q", info.CWD, tt.cwd)
			}
			if info.FirstPrompt != tt.firstPrompt {
				t.Errorf("FirstPrompt = %q, want %q", info.FirstPrompt, tt.firstPrompt)
			}
			if info.TodoDone != tt.done || info.TodoTotal != tt.total {
				t.Errorf("todos = %d/%d, want %d/%d", info.TodoDone, info.TodoTotal, tt.done, tt.total)
			}
			if info.LastAssistant != tt.lastAssistant {
				t.Errorf("LastAssistant = %q, want %q", info.LastAssistant, tt.lastAssistant)
			}
			if tt.lastActivity != "" {
				want, _ := time.Parse(time.RFC3339, tt.lastActivity)
				if !info.LastActivity.Equal(want) {
					t.Errorf("LastActivity = %v, want %v", info.LastActivity, want)
				}
			}
		})
	}
}

func TestParseFileUsesFileNameAndMtimeFallback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc-123.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"user","cwd":"/x","message":{"role":"user","content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Date(2025, 5, 5, 5, 5, 5, 0, time.UTC)
	os.Chtimes(p, mt, mt)
	info, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != "abc-123" || info.CWD != "/x" || info.FirstPrompt != "hi" {
		t.Errorf("got %+v", info)
	}
	if !info.LastActivity.Equal(mt) {
		t.Errorf("LastActivity = %v, want mtime %v", info.LastActivity, mt)
	}
}

func TestParseSkipsOverlongLine(t *testing.T) {
	long := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", MaxLineSize) + `"}}`
	in := long + "\n" + `{"type":"user","message":{"role":"user","content":"second"}}` + "\n"
	info, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if info.FirstPrompt != "second" {
		t.Errorf("FirstPrompt = %q", info.FirstPrompt)
	}
}

func TestParseNoTrailingNewline(t *testing.T) {
	info, _ := Parse(strings.NewReader(`{"type":"user","message":{"role":"user","content":"last"}}`))
	if info.FirstPrompt != "last" {
		t.Errorf("FirstPrompt = %q", info.FirstPrompt)
	}
}

func TestTodoWriteReplacesTaskList(t *testing.T) {
	in := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"TaskCreate","input":{"subject":"s"}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b","name":"TodoWrite","input":{"todos":[{"content":"x","status":"completed"},{"content":"y","status":"pending"}]}}]}}
`
	info, _ := Parse(strings.NewReader(in))
	if info.TodoDone != 1 || info.TodoTotal != 2 {
		t.Errorf("todos = %d/%d", info.TodoDone, info.TodoTotal)
	}
}

func TestCacheReparsesOnlyOnChange(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-home-dev-x")
	os.MkdirAll(filepath.Join(proj, "sess", "subagents"), 0o755)
	p := filepath.Join(proj, "sess.jsonl")
	os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"one"}}`+"\n"), 0o644)
	// Subagent transcripts must not count as sessions.
	os.WriteFile(filepath.Join(proj, "sess", "subagents", "agent-1.jsonl"), []byte("{}\n"), 0o644)

	c := NewCache()
	infos, err := c.Scan(root)
	if err != nil || len(infos) != 1 {
		t.Fatalf("Scan: %v %v", infos, err)
	}
	c.Scan(root)
	if c.Parses() != 1 {
		t.Fatalf("parses = %d, want 1", c.Parses())
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"two"}]}}` + "\n")
	f.Close()
	infos, _ = c.Scan(root)
	if c.Parses() != 2 || infos[0].LastAssistant != "two" {
		t.Fatalf("parses = %d, info = %+v", c.Parses(), infos[0])
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 60); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("héllo wörld", 6); got != "héllo…" {
		t.Errorf("got %q", got)
	}
}
