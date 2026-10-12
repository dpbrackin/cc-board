package main

import (
	"fmt"
	"io"

	"github.com/dpbrackin/cc-board/internal/config"
)

func runTUI(cfg config.Config, stderr io.Writer) int {
	fmt.Fprintln(stderr, "cc-board: TUI not implemented yet; use `cc-board list`")
	return 1
}
