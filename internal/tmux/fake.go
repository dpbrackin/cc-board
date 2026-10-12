package tmux

import (
	"errors"
	"strings"
	"sync"
)

// FakeRunner records calls and answers display-message queries from Panes.
// It is used by tests in this and other packages.
type FakeRunner struct {
	mu sync.Mutex
	// Panes maps pane ID to its current command. Missing panes fail.
	Panes map[string]string
	// Err, if set, is returned for every non-query command.
	Err   error
	Calls [][]string
}

// Run records args and simulates tmux.
func (f *FakeRunner) Run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "display-message" {
		pane := ""
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				pane = args[i+1]
			}
		}
		cmd, ok := f.Panes[pane]
		if !ok {
			return "", errors.New("can't find pane: " + pane)
		}
		return cmd + "\n", nil
	}
	return "", f.Err
}

// Commands returns the recorded calls joined with spaces.
func (f *FakeRunner) Commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = strings.Join(c, " ")
	}
	return out
}
