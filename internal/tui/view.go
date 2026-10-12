package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/dpbrackin/cc-board/internal/session"
)

const (
	badgeWidth    = 11 // "● needs you"
	progressWidth = 12 // "█████  100%" or "█████  12/20"
	timeWidth     = 8
	hintWidth     = 5
	barCells      = 5
)

// View renders the board.
func (m Model) View() string {
	w := m.width
	if w < 40 {
		w = 40
	}
	var b strings.Builder
	b.WriteString(m.header(w))
	b.WriteByte('\n')
	rule := styleDim.Render(" " + strings.Repeat("─", w-2))
	b.WriteString(rule + "\n")

	lines := m.listLines(w)
	h := m.listHeight()
	for i := 0; i < h; i++ {
		if i < len(lines) {
			b.WriteString(lines[i])
		}
		b.WriteByte('\n')
	}
	if m.preview {
		b.WriteString(rule + "\n")
		for _, l := range strings.Split(m.viewport.View(), "\n") {
			b.WriteString(" " + l + "\n")
		}
	}
	b.WriteString(rule + "\n")
	b.WriteString(m.footer(w))
	return b.String()
}

func (m Model) header(w int) string {
	c := session.Count(m.visible())
	left := " " + styleTitle.Render("cc-board")
	if m.showAll {
		left += styleDim.Render("  (all)")
	}
	parts := []string{
		styleNeeds.Render(fmt.Sprintf("%d needs you", c.NeedsInput)),
		styleWorking.Render(fmt.Sprintf("%d working", c.Working)),
		fmt.Sprintf("%d idle", c.Idle),
		styleClosed.Render(fmt.Sprintf("%d closed", c.Closed)),
	}
	right := strings.Join(parts, styleDim.Render(" · ")) + " "
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) listLines(w int) []string {
	if !m.loaded {
		return []string{styleDim.Render(" loading…")}
	}
	if m.loadErr != nil && len(m.rows) == 0 {
		return []string{styleError.Render(" " + m.loadErr.Error())}
	}
	if len(m.rows) == 0 {
		msg := " no sessions"
		if m.filter != "" {
			msg = " no sessions match " + fmt.Sprintf("%q", m.filter)
		} else if !m.showAll {
			msg += " · a shows hidden and old closed sessions"
		}
		return []string{styleDim.Render(msg)}
	}
	h := m.listHeight()
	end := m.offset + h
	if end > len(m.rows) {
		end = len(m.rows)
	}
	lines := make([]string, 0, end-m.offset)
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		if r.header {
			lines = append(lines, m.groupLine(r.group, w))
		} else {
			lines = append(lines, m.sessionLine(r.session, w, i == m.cursor))
		}
	}
	return lines
}

func (m Model) groupLine(g session.Group, w int) string {
	name := " " + styleGroup.Render(g.Name)
	path := session.ShortenHome(g.Dir)
	room := w - lipgloss.Width(name) - 3
	path = runewidth.Truncate(path, max(room, 0), "…")
	missing := len(g.Sessions) > 0 && g.Sessions[0].CWDMissing
	if missing {
		return name + "  " + styleStrike.Render(path)
	}
	return name + "  " + styleDim.Render(path)
}

func (m Model) sessionLine(s session.Session, w int, selected bool) string {
	bd := badgeFor(s)
	now := m.opt.Now()

	badgeText := pad(bd.icon+" "+bd.label, badgeWidth)
	progress := ""
	if s.HasProgress() {
		filled := s.Percent() * barCells / 100
		progress = strings.Repeat("█", filled) + strings.Repeat("░", barCells-filled) + "  " + s.ProgressLabel()
	}
	progress = pad(progress, progressWidth)
	rel := pad(session.RelTime(s.UpdatedAt, now), timeWidth)
	hint := pad("", hintWidth)
	if s.DoneHint() {
		hint = "done?"
	}

	// " " badge "  " title "  " progress "  " time "  " hint
	fixed := 1 + badgeWidth + 2 + 2 + progressWidth + 2 + timeWidth + 2 + hintWidth + 1
	titleW := w - fixed
	if titleW < 10 {
		titleW = 10
	}
	renaming := m.mode == modeRename && m.renaming == s.ID
	title, note := s.Title, ""
	if renaming {
		m.input.Width = titleW - 1
		title = pad(m.input.View(), titleW)
	} else {
		title, note = titleAndNote(s.Title, s.Note(), titleW)
	}

	if selected && !renaming {
		line := " " + badgeText + "  " + title + note + "  " + progress + "  " + rel + "  " + hint + " "
		return styleSelected.Render(line)
	}
	titleStyle := lipgloss.NewStyle()
	if s.Status == session.Closed || s.Done {
		titleStyle = styleClosed
	}
	return " " + bd.style.Render(badgeText) + "  " + titleStyle.Render(title) + styleDim.Render(note) + "  " + progress + "  " +
		styleDim.Render(rel) + "  " + styleDim.Render(hint) + " "
}

func (m Model) footer(w int) string {
	switch m.mode {
	case modeFilter:
		return " " + stylePromptText.Render("/") + m.input.View() + styleDim.Render("   enter keep · esc clear")
	case modeRename:
		return " " + stylePromptText.Render("rename") + styleDim.Render("   enter save · esc cancel · empty resets")
	}
	if m.message != "" {
		if m.messageErr {
			return " " + styleError.Render(m.message)
		}
		return " " + styleDim.Render(m.message)
	}
	help := "enter jump · o open dir · r rename · d done · x hide · a all · / filter · p preview · q quit"
	if m.filter != "" {
		help = fmt.Sprintf("filter %q · esc clear · ", m.filter) + help
	}
	if !m.opt.Client.InTmux {
		help = "not in tmux: live jump unavailable · " + help
	}
	return " " + styleDim.Render(runewidth.Truncate(help, w-2, "…"))
}

// titleAndNote fits the title and an optional progress note into w cells.
// The note follows the title after " · " and takes the room the title doesn't
// need, but the title always keeps at least half the column. The two returned
// strings together are exactly w cells wide.
func titleAndNote(title, note string, w int) (string, string) {
	const sep = " · "
	sepW := runewidth.StringWidth(sep)
	if note == "" {
		return pad(runewidth.Truncate(title, w, "…"), w), ""
	}
	room := w - sepW - runewidth.StringWidth(note)
	if room < w/2 {
		room = w / 2
	}
	short := title
	if runewidth.StringWidth(short) > room {
		short = runewidth.Truncate(short, room, "…")
	}
	noteW := w - runewidth.StringWidth(short)
	if noteW < sepW+3 {
		// Too narrow for a useful note: give the whole column to the title.
		return pad(runewidth.Truncate(title, w, "…"), w), ""
	}
	return short, pad(runewidth.Truncate(sep+note, noteW, "…"), noteW)
}

// pad pads s with spaces to display width n (it never truncates styled text).
func pad(s string, n int) string {
	if d := n - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// wrap word-wraps text to width w, keeping existing line breaks.
func wrap(text string, w int) string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for runewidth.StringWidth(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				head := runewidth.Truncate(word, w, "")
				out = append(out, head)
				word = word[len(head):]
			}
			switch {
			case line == "":
				line = word
			case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
