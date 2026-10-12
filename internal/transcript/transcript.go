// Package transcript parses Claude Code session transcripts
// (<claude_dir>/projects/<encoded-path>/<session_id>.jsonl).
//
// The format is internal to Claude Code and changes over time, so the parser
// is deliberately tolerant: unknown lines, entry types and fields are skipped.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Todo tool names. Older Claude Code versions write the whole list with
// TodoWriteTools; newer ones create and update tasks one at a time with
// TaskCreateTools / TaskUpdateTools. Add new names here.
var (
	TodoWriteTools  = []string{"TodoWrite"}
	TaskCreateTools = []string{"TaskCreate"}
	TaskUpdateTools = []string{"TaskUpdate"}
)

// MaxLineSize is the longest transcript line that is parsed; longer lines are skipped.
const MaxLineSize = 10 * 1024 * 1024

// TitleMax is the maximum title length in characters.
const TitleMax = 60

// Info is what cc-board extracts from one transcript.
type Info struct {
	Path          string
	SessionID     string
	CWD           string
	FirstPrompt   string    // first real user prompt, truncated to TitleMax
	TodoDone      int       // completed todos in the latest list
	TodoTotal     int       // total todos in the latest list (0 = no list)
	LastActivity  time.Time // last timestamp, or file mtime
	LastAssistant string    // last assistant text block
}

// HasTodos reports whether the session has a todo list.
func (i Info) HasTodos() bool { return i.TodoTotal > 0 }

// entry is the subset of a transcript line that the parser reads.
type entry struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
	ToolResult  json.RawMessage `json:"toolUseResult"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type todo struct {
	ID     string
	Status string
}

// parser accumulates state while reading lines.
type parser struct {
	info     Info
	lastTS   time.Time
	todos    []todo
	pending  map[string]int // TaskCreate tool_use id -> index in todos
	nextTask int
}

// ParseFile parses the transcript at path.
func ParseFile(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	info, err := Parse(f)
	info.Path = path
	// The file name is the session ID; prefer it, it is what --resume takes.
	if base := strings.TrimSuffix(filepath.Base(path), ".jsonl"); base != "" {
		info.SessionID = base
	}
	if info.LastActivity.IsZero() {
		info.LastActivity = fi.ModTime()
	}
	return info, err
}

// Parse reads a transcript stream. It never fails on bad content; it returns
// an error only for read errors, along with whatever was parsed so far.
func Parse(r io.Reader) (Info, error) {
	p := &parser{pending: map[string]int{}}
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	tooLong := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 && !tooLong {
			if len(buf)+len(chunk) > MaxLineSize {
				tooLong = true
				buf = buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if !isPrefix && err == nil {
			if !tooLong {
				p.line(buf)
			}
			buf = buf[:0]
			tooLong = false
		}
		if err != nil {
			if !tooLong && len(buf) > 0 {
				p.line(buf)
			}
			p.finish()
			if errors.Is(err, io.EOF) {
				return p.info, nil
			}
			return p.info, err
		}
	}
}

func (p *parser) finish() {
	p.info.LastActivity = p.lastTS
	if len(p.todos) > 0 {
		p.info.TodoTotal = len(p.todos)
		p.info.TodoDone = 0
		for _, t := range p.todos {
			if t.Status == "completed" {
				p.info.TodoDone++
			}
		}
	}
}

func (p *parser) line(b []byte) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return
	}
	var e entry
	if err := json.Unmarshal(b, &e); err != nil {
		return
	}
	if e.SessionID != "" && p.info.SessionID == "" {
		p.info.SessionID = e.SessionID
	}
	if e.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.After(p.lastTS) {
			p.lastTS = ts
		}
	}
	if e.IsSidechain {
		return
	}
	if p.info.CWD == "" && e.CWD != "" {
		p.info.CWD = e.CWD
	}
	if len(e.Message) == 0 {
		return
	}
	var m message
	if err := json.Unmarshal(e.Message, &m); err != nil {
		return
	}
	switch e.Type {
	case "user":
		p.user(e, m)
	case "assistant":
		p.assistant(m)
	}
}

func (p *parser) user(e entry, m message) {
	blocks, text := decodeContent(m.Content)
	// Tool results: map TaskCreate results to their task IDs.
	for _, b := range blocks {
		if b.Type == "tool_result" {
			if idx, ok := p.pending[b.ToolUseID]; ok {
				if id := taskIDFromResult(e.ToolResult, b.Content); id != "" {
					p.todos[idx].ID = id
				}
				delete(p.pending, b.ToolUseID)
			}
		}
	}
	if p.info.FirstPrompt != "" || e.IsMeta {
		return
	}
	if text != "" {
		p.prompt(text)
		return
	}
	for _, b := range blocks {
		if b.Type == "text" && p.prompt(b.Text) {
			return
		}
	}
}

// prompt records s as the first prompt if it is a real user prompt.
func (p *parser) prompt(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || isWrapper(s) {
		return false
	}
	p.info.FirstPrompt = Truncate(strings.Join(strings.Fields(s), " "), TitleMax)
	return true
}

// wrapperPrefixes mark user content that Claude Code generates rather than
// the user typing it: slash-command wrappers, command output, reminders.
var wrapperPrefixes = []string{
	"<command-name>", "<command-message>", "<command-args>",
	"<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>",
	"<system-reminder>", "<user-prompt-submit-hook>", "<bash-input>", "<bash-stdout>", "<bash-stderr>",
	"Caveat: The messages below were generated by the user while running local commands",
	"[Request interrupted by user",
}

func isWrapper(s string) bool {
	for _, p := range wrapperPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func (p *parser) assistant(m message) {
	blocks, text := decodeContent(m.Content)
	if strings.TrimSpace(text) != "" {
		p.info.LastAssistant = strings.TrimSpace(text)
	}
	for _, b := range blocks {
		switch {
		case b.Type == "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				p.info.LastAssistant = t
			}
		case b.Type == "tool_use" && contains(TodoWriteTools, b.Name):
			p.todoWrite(b.Input)
		case b.Type == "tool_use" && contains(TaskCreateTools, b.Name):
			p.taskCreate(b)
		case b.Type == "tool_use" && contains(TaskUpdateTools, b.Name):
			p.taskUpdate(b.Input)
		}
	}
}

func (p *parser) todoWrite(raw json.RawMessage) {
	var in struct {
		Todos []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"todos"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return
	}
	p.todos = p.todos[:0]
	p.pending = map[string]int{}
	p.nextTask = 0
	for _, t := range in.Todos {
		p.todos = append(p.todos, todo{ID: t.ID, Status: t.Status})
	}
}

func (p *parser) taskCreate(b block) {
	p.nextTask++
	// Task IDs are assigned sequentially; the tool result, if it carries an
	// ID, overrides this guess.
	p.todos = append(p.todos, todo{ID: itoa(p.nextTask), Status: "pending"})
	if b.ID != "" {
		p.pending[b.ID] = len(p.todos) - 1
	}
}

func (p *parser) taskUpdate(raw json.RawMessage) {
	var in struct {
		TaskID json.RawMessage `json:"taskId"`
		Status string          `json:"status"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Status == "" {
		return
	}
	id := rawID(in.TaskID)
	for i := range p.todos {
		if p.todos[i].ID == id {
			if in.Status == "deleted" {
				p.todos = append(p.todos[:i], p.todos[i+1:]...)
				for k, v := range p.pending {
					if v > i {
						p.pending[k] = v - 1
					} else if v == i {
						delete(p.pending, k)
					}
				}
			} else {
				p.todos[i].Status = in.Status
			}
			return
		}
	}
}

var taskNumRE = regexp.MustCompile(`#(\d+)`)

// taskIDFromResult extracts a task ID from a TaskCreate result: either a
// structured toolUseResult ({"task":{"id":"3"}} or {"taskId":"3"}) or text
// such as "Task #3 created".
func taskIDFromResult(structured, content json.RawMessage) string {
	if len(structured) > 0 {
		var r struct {
			Task struct {
				ID json.RawMessage `json:"id"`
			} `json:"task"`
			TaskID json.RawMessage `json:"taskId"`
			ID     json.RawMessage `json:"id"`
		}
		if json.Unmarshal(structured, &r) == nil {
			for _, c := range []json.RawMessage{r.Task.ID, r.TaskID, r.ID} {
				if id := rawID(c); id != "" {
					return id
				}
			}
		}
	}
	blocks, text := decodeContent(content)
	for _, b := range blocks {
		text += " " + b.Text
	}
	if m := taskNumRE.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// rawID turns a JSON string or number into a string.
func rawID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// decodeContent handles content that is either a string or a block array.
func decodeContent(raw json.RawMessage) ([]block, string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return nil, s
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil, ""
		}
		blocks := make([]block, 0, len(items))
		for _, it := range items {
			var b block
			if json.Unmarshal(it, &b) == nil {
				blocks = append(blocks, b)
			}
		}
		return blocks, ""
	}
	return nil, ""
}

// Truncate shortens s to at most max characters, ending with "…" if cut.
func Truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }
