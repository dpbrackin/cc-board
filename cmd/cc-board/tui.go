package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dpbrackin/cc-board/internal/config"
	"github.com/dpbrackin/cc-board/internal/store"
	"github.com/dpbrackin/cc-board/internal/tmux"
	"github.com/dpbrackin/cc-board/internal/tui"
)

func runTUI(cfg config.Config, stderr io.Writer) int {
	client := newClient(cfg)
	model := tui.New(tui.Options{
		Loader:          newLoader(cfg, client),
		Store:           store.New(cfg.BoardDir()),
		Client:          client,
		PollInterval:    cfg.PollInterval,
		HideClosedAfter: cfg.HideClosedAfter,
	})
	final, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(stderr, "cc-board:", err)
		return 1
	}
	if m, ok := final.(tui.Model); ok && m.Exec() != nil {
		// Outside tmux: replace this process with the resume command or shell.
		if err := execIn(m.Exec()); err != nil {
			fmt.Fprintln(stderr, "cc-board:", err)
			return 1
		}
	}
	return 0
}

func execIn(spec *tmux.ExecSpec) error {
	if err := os.Chdir(spec.Dir); err != nil {
		return err
	}
	path, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return err
	}
	env := os.Environ()
	env = append(env, "PWD="+spec.Dir)
	return syscall.Exec(path, spec.Argv, env)
}
