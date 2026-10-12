// Command cc-board shows every Claude Code session on the machine in one TUI.
//
// Usage:
//
//	cc-board            open the TUI
//	cc-board hook       called by Claude Code hooks (reads event JSON on stdin)
//	cc-board list       print sessions as plain text
//	cc-board version    print version
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dpbrackin/cc-board/internal/config"
	"github.com/dpbrackin/cc-board/internal/hook"
	"github.com/dpbrackin/cc-board/internal/session"
	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/tmux"
	"github.com/dpbrackin/cc-board/internal/transcript"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: cc-board [command] [flags]

Commands:
  (none)    open the TUI
  hook      called by Claude Code hooks; reads event JSON on stdin
  list      print sessions as plain text
  version   print version

Flags:
  -config path   config file (default ~/.config/cc-board/config.json)
`)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	if cmd == "hook" {
		// The hook must never write to stdout and must always exit 0.
		return runHook(args, stdin)
	}

	fs := flag.NewFlagSet("cc-board", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr) }
	configPath := fs.String("config", "", "config file")
	all := false
	if cmd == "list" {
		fs.BoolVar(&all, "a", false, "include hidden and old closed sessions")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "cc-board", version)
		return 0
	case "help":
		usage(stdout)
		return 0
	case "", "list":
	default:
		fmt.Fprintf(stderr, "cc-board: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "cc-board:", err)
		return 1
	}
	if cmd == "list" {
		return runList(cfg, all, stdout, stderr)
	}
	return runTUI(cfg, stderr)
}

func runHook(args []string, stdin io.Reader) int {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	_ = fs.Parse(args)
	// Only the claude_dir matters here; a bad config file must not break the hook.
	cfg, err := config.Load(*configPath)
	if err != nil {
		cfg = config.Default()
	}
	hook.Run(cfg.BoardDir(), stdin)
	return 0
}

func newClient(cfg config.Config) tmux.Client {
	return tmux.Client{
		R:             tmux.ExecRunner{},
		LiveCommands:  cfg.LiveCommands,
		InTmux:        tmux.InTmux(),
		ResumeCommand: cfg.ResumeCommand,
	}
}

func newLoader(cfg config.Config, client tmux.Client) *session.Loader {
	return &session.Loader{
		ProjectsDir: cfg.ProjectsDir(),
		Store:       store.New(cfg.BoardDir()),
		Cache:       transcript.NewCache(),
		Live:        client.IsLive,
	}
}

func runList(cfg config.Config, all bool, stdout, stderr io.Writer) int {
	loader := newLoader(cfg, newClient(cfg))
	sessions, err := loader.Load()
	if err != nil {
		fmt.Fprintln(stderr, "cc-board:", err)
		return 1
	}
	now := time.Now()
	groups := session.GroupAndSort(session.Visible(sessions, all, cfg.HideClosedAfter, now))
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, g := range groups {
		fmt.Fprintf(w, "%s  %s\n", g.Name, session.ShortenHome(g.Dir))
		for _, s := range g.Sessions {
			status := s.Status
			if s.Done {
				status = "done"
			}
			progress := ""
			if s.HasProgress() {
				progress = fmt.Sprintf("%d/%d", s.TodoDone, s.TodoTotal)
			}
			hint := ""
			if s.DoneHint() {
				hint = "done?"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", status, transcript.Truncate(s.Title, 60), progress, session.RelTime(s.UpdatedAt, now), s.ID, hint)
		}
	}
	w.Flush()
	return 0
}
