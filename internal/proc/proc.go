// Package proc looks up processes by PID: their name and parent. It reads
// /proc where it exists (Linux) and falls back to ps (macOS and other Unix).
//
// cc-board uses it to find the Claude Code process that ran a command (the
// hook, or `cc-board progress`) by walking up the parent chain, and to check
// later whether that process is still running.
package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Info describes one process.
type Info struct {
	PID  int
	PPID int
	Name string // executable base name, e.g. "claude" or "node"
}

// ErrNotFound means no process has that PID.
var ErrNotFound = errors.New("proc: no such process")

// LookupFunc returns information about a process.
type LookupFunc func(pid int) (Info, error)

// Lookup returns information about pid using /proc, or ps where /proc is missing.
func Lookup(pid int) (Info, error) {
	if pid <= 0 {
		return Info{}, ErrNotFound
	}
	if hasProc() {
		return lookupProc(pid)
	}
	return lookupPS(pid)
}

func hasProc() bool {
	_, err := os.Stat("/proc/self/stat")
	return err == nil
}

// lookupProc parses /proc/<pid>/stat: "pid (comm) state ppid ...". comm may
// contain spaces and parentheses, so the fields after it are found from the
// last ')'.
func lookupProc(pid int) (Info, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return Info{}, ErrNotFound
	}
	return parseStat(pid, string(data))
}

func parseStat(pid int, stat string) (Info, error) {
	open := strings.IndexByte(stat, '(')
	end := strings.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return Info{}, fmt.Errorf("proc: bad stat for %d", pid)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return Info{}, fmt.Errorf("proc: bad stat for %d", pid)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Info{}, fmt.Errorf("proc: bad ppid for %d", pid)
	}
	return Info{PID: pid, PPID: ppid, Name: stat[open+1 : end]}, nil
}

func lookupPS(pid int) (Info, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return Info{}, ErrNotFound
	}
	return parsePS(pid, string(out))
}

// parsePS parses one line of `ps -o ppid= -o comm=`. On macOS comm is the
// full executable path, so only its base name is kept.
func parsePS(pid int, line string) (Info, error) {
	line = strings.TrimSpace(line)
	ppidStr, comm, ok := strings.Cut(line, " ")
	if !ok {
		return Info{}, ErrNotFound
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return Info{}, fmt.Errorf("proc: bad ps output for %d", pid)
	}
	return Info{PID: pid, PPID: ppid, Name: filepath.Base(strings.TrimSpace(comm))}, nil
}

// Matches reports whether name is one of names.
func Matches(name string, names []string) bool {
	for _, n := range names {
		if name == n {
			return true
		}
	}
	return false
}

// maxDepth bounds the parent walk; Claude Code is normally 1-3 levels up.
const maxDepth = 10

// FindAncestor walks up from start (inclusive) and returns the nearest process
// whose name is in names. ok is false if none is found before PID 1.
func FindAncestor(lookup LookupFunc, start int, names []string) (Info, bool) {
	if lookup == nil {
		lookup = Lookup
	}
	pid := start
	for i := 0; i < maxDepth && pid > 1; i++ {
		info, err := lookup(pid)
		if err != nil {
			return Info{}, false
		}
		if Matches(info.Name, names) {
			return info, true
		}
		pid = info.PPID
	}
	return Info{}, false
}

// Alive reports whether pid is running and its name is still in names. The
// name check guards against the PID being reused by an unrelated process.
func Alive(lookup LookupFunc, pid int, names []string) bool {
	if lookup == nil {
		lookup = Lookup
	}
	info, err := lookup(pid)
	return err == nil && Matches(info.Name, names)
}
