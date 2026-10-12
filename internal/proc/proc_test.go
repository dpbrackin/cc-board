package proc

import (
	"os"
	"testing"
)

func fakeTree(procs ...Info) LookupFunc {
	m := map[int]Info{}
	for _, p := range procs {
		m[p.PID] = p
	}
	return func(pid int) (Info, error) {
		if p, ok := m[pid]; ok {
			return p, nil
		}
		return Info{}, ErrNotFound
	}
}

var names = []string{"claude", "node"}

func TestFindAncestor(t *testing.T) {
	tree := fakeTree(
		Info{PID: 1, PPID: 0, Name: "launchd"},
		Info{PID: 100, PPID: 1, Name: "zsh"},
		Info{PID: 200, PPID: 100, Name: "claude"},
		Info{PID: 300, PPID: 200, Name: "sh"},
		Info{PID: 400, PPID: 300, Name: "cc-board"},
		Info{PID: 500, PPID: 100, Name: "vim"},
	)
	if got, ok := FindAncestor(tree, 400, names); !ok || got.PID != 200 {
		t.Errorf("from hook: got %+v, %v; want 200", got, ok)
	}
	if got, ok := FindAncestor(tree, 200, names); !ok || got.PID != 200 {
		t.Errorf("start is itself claude: got %+v, %v", got, ok)
	}
	if _, ok := FindAncestor(tree, 500, names); ok {
		t.Error("vim has no claude ancestor")
	}
	if _, ok := FindAncestor(tree, 999, names); ok {
		t.Error("unknown pid")
	}
}

func TestFindAncestorStopsOnLoops(t *testing.T) {
	loop := fakeTree(Info{PID: 10, PPID: 11, Name: "a"}, Info{PID: 11, PPID: 10, Name: "b"})
	if _, ok := FindAncestor(loop, 10, names); ok {
		t.Error("loop should not match")
	}
}

func TestAlive(t *testing.T) {
	tree := fakeTree(Info{PID: 200, PPID: 1, Name: "claude"}, Info{PID: 201, PPID: 1, Name: "python3"})
	if !Alive(tree, 200, names) {
		t.Error("200 runs claude")
	}
	if Alive(tree, 201, names) {
		t.Error("201 was reused by python3")
	}
	if Alive(tree, 202, names) {
		t.Error("202 does not exist")
	}
}

func TestParseStat(t *testing.T) {
	got, err := parseStat(42, "42 (my (odd) name) S 7 42 42 0 -1 4194560")
	if err != nil || got.PPID != 7 || got.Name != "my (odd) name" {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := parseStat(1, "garbage"); err == nil {
		t.Error("want error")
	}
}

func TestParsePS(t *testing.T) {
	got, err := parsePS(42, "  7 /Users/me/.local/bin/claude\n")
	if err != nil || got.PPID != 7 || got.Name != "claude" {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := parsePS(42, ""); err == nil {
		t.Error("want error for empty output")
	}
}

func TestLookupSelf(t *testing.T) {
	// Exercises the real /proc or ps path on this machine.
	info, err := Lookup(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if info.PPID != os.Getppid() || info.Name == "" {
		t.Errorf("got %+v, want ppid %d", info, os.Getppid())
	}
	if _, err := lookupPS(os.Getpid()); err != nil {
		t.Errorf("ps fallback: %v", err)
	}
}
