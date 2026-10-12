// Package store reads and writes the per-session status files (written by the
// hook) and meta files (written by the TUI) under <claude_dir>/board.
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status values written by the hook.
const (
	StatusWorking    = "working"
	StatusNeedsInput = "needs_input"
	StatusIdle       = "idle"
	StatusEnded      = "ended"
)

// Status is the content of sessions/<session_id>.json.
type Status struct {
	SessionID      string    `json:"session_id"`
	CWD            string    `json:"cwd"`
	TranscriptPath string    `json:"transcript_path"`
	TmuxPane       string    `json:"tmux_pane"`
	Status         string    `json:"status"`
	LastEvent      string    `json:"last_event"`
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Meta is the content of meta/<session_id>.json.
type Meta struct {
	Label  string `json:"label,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// IsZero reports whether m carries no user data.
func (m Meta) IsZero() bool { return m == Meta{} }

// Store addresses the board directory.
type Store struct {
	Dir string // <claude_dir>/board
}

// New returns a Store rooted at dir.
func New(dir string) *Store { return &Store{Dir: dir} }

// SessionsDir is <board>/sessions.
func (s *Store) SessionsDir() string { return filepath.Join(s.Dir, "sessions") }

// MetaDir is <board>/meta.
func (s *Store) MetaDir() string { return filepath.Join(s.Dir, "meta") }

// LogPath is <board>/hook.log.
func (s *Store) LogPath() string { return filepath.Join(s.Dir, "hook.log") }

func (s *Store) statusPath(id string) string {
	return filepath.Join(s.SessionsDir(), id+".json")
}

func (s *Store) metaPath(id string) string {
	return filepath.Join(s.MetaDir(), id+".json")
}

// ValidID reports whether id is safe to use as a file name.
func ValidID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`+"\x00")
}

// ReadStatus returns the status file for id. ok is false if it does not exist.
func (s *Store) ReadStatus(id string) (st Status, ok bool, err error) {
	if !ValidID(id) {
		return st, false, errors.New("store: invalid session id")
	}
	ok, err = readJSON(s.statusPath(id), &st)
	return st, ok, err
}

// WriteStatus atomically writes the status file for st.SessionID.
func (s *Store) WriteStatus(st Status) error {
	if !ValidID(st.SessionID) {
		return errors.New("store: invalid session id")
	}
	return writeJSON(s.statusPath(st.SessionID), st)
}

// ReadAllStatus reads every status file. Unreadable files are skipped.
func (s *Store) ReadAllStatus() (map[string]Status, error) {
	out := map[string]Status{}
	err := readDir(s.SessionsDir(), func(id, path string) {
		var st Status
		if ok, err := readJSON(path, &st); ok && err == nil {
			if st.SessionID == "" {
				st.SessionID = id
			}
			out[st.SessionID] = st
		}
	})
	return out, err
}

// ReadMeta returns the meta for id (zero value if absent).
func (s *Store) ReadMeta(id string) (Meta, error) {
	var m Meta
	if !ValidID(id) {
		return m, errors.New("store: invalid session id")
	}
	_, err := readJSON(s.metaPath(id), &m)
	return m, err
}

// WriteMeta atomically writes the meta file for id.
func (s *Store) WriteMeta(id string, m Meta) error {
	if !ValidID(id) {
		return errors.New("store: invalid session id")
	}
	return writeJSON(s.metaPath(id), m)
}

// ReadAllMeta reads every meta file. Unreadable files are skipped.
func (s *Store) ReadAllMeta() (map[string]Meta, error) {
	out := map[string]Meta{}
	err := readDir(s.MetaDir(), func(id, path string) {
		var m Meta
		if ok, err := readJSON(path, &m); ok && err == nil {
			out[id] = m
		}
	})
	return out, err
}

func readDir(dir string, fn func(id, path string)) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		fn(strings.TrimSuffix(name, ".json"), filepath.Join(dir, name))
	}
	return nil
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, err
	}
	return true, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// WriteFileAtomic writes data to a temp file in the same directory and renames
// it over path, creating the directory if needed.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
