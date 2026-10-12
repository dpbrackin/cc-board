package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/dpbrackin/cc-board/internal/session"
)

// Styles use the terminal's default palette (ANSI colors), no custom theme.
var (
	styleTitle      = lipgloss.NewStyle().Bold(true)
	styleDim        = lipgloss.NewStyle().Faint(true)
	styleNeeds      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleWorking    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleIdle       = lipgloss.NewStyle()
	styleClosed     = lipgloss.NewStyle().Faint(true)
	styleGroup      = lipgloss.NewStyle().Bold(true)
	styleStrike     = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	styleSelected   = lipgloss.NewStyle().Reverse(true)
	styleError      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stylePromptText = lipgloss.NewStyle().Bold(true)
)

type badge struct {
	icon, label string
	style       lipgloss.Style
}

func badgeFor(s session.Session) badge {
	b := statusBadge(s.Status)
	if s.Done {
		// Done keeps the status label so a done-but-live session is visible.
		b.icon, b.style = "✓", styleClosed
	}
	return b
}

func statusBadge(status string) badge {
	switch status {
	case session.NeedsInput:
		return badge{"●", "needs you", styleNeeds}
	case session.Working:
		return badge{"◐", "working", styleWorking}
	case session.Idle:
		return badge{"○", "idle", styleIdle}
	default:
		return badge{"◌", "closed", styleClosed}
	}
}
