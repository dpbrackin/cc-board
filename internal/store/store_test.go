package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusRoundTrip(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "board"))
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := Status{SessionID: "abc", CWD: "/p", Status: StatusWorking, StartedAt: now, UpdatedAt: now}
	if err := s.WriteStatus(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ReadStatus("abc")
	if err != nil || !ok {
		t.Fatalf("ReadStatus: ok=%v err=%v", ok, err)
	}
	if !got.UpdatedAt.Equal(now) || got.Status != StatusWorking || got.CWD != "/p" {
		t.Errorf("got %+v", got)
	}
	all, err := s.ReadAllStatus()
	if err != nil || len(all) != 1 {
		t.Fatalf("ReadAllStatus: %v %v", all, err)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(s.SessionsDir())
	if len(entries) != 1 {
		t.Errorf("expected 1 file, got %d", len(entries))
	}
}

func TestMetaRoundTripAndMissing(t *testing.T) {
	s := New(t.TempDir())
	m, err := s.ReadMeta("none")
	if err != nil || !m.IsZero() {
		t.Fatalf("missing meta: %+v %v", m, err)
	}
	if err := s.WriteMeta("x", Meta{Label: "L", Done: true}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ReadAllMeta()
	if err != nil || all["x"].Label != "L" || !all["x"].Done {
		t.Fatalf("ReadAllMeta: %+v %v", all, err)
	}
}

func TestInvalidID(t *testing.T) {
	s := New(t.TempDir())
	if err := s.WriteStatus(Status{SessionID: "../evil"}); err == nil {
		t.Fatal("expected error for path traversal id")
	}
}

func TestReadAllSkipsCorrupt(t *testing.T) {
	s := New(t.TempDir())
	os.MkdirAll(s.SessionsDir(), 0o755)
	os.WriteFile(filepath.Join(s.SessionsDir(), "bad.json"), []byte("{"), 0o644)
	if err := s.WriteStatus(Status{SessionID: "good"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ReadAllStatus()
	if err != nil || len(all) != 1 {
		t.Fatalf("got %v %v", all, err)
	}
}
