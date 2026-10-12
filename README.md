# cc-board

A board for all your Claude Code sessions. `cc-board` lists every session on the
machine in one terminal UI, grouped by project, with live status (needs you,
working, idle, closed) and progress, either reported by the session itself or
read from Claude's task list. Press enter on a row to go to that
session: it switches to the session's tmux pane, or, if the session has closed,
resumes it in a new tmux window.

```
 cc-board                         1 needs you · 2 working · 4 idle · 6 closed
 ─────────────────────────────────────────────────────────────────────────────
 api  ~/projects/api
 ● needs you   Add rate limiting · writing tests    ███░░  60%    just now
 ◐ working     Fix flaky integration tests          █░░░░  1/4    2m ago
 web  ~/projects/web
 ○ idle        Migrate settings page to new forms   █████  6/6    18m ago  done?
 ◌ closed      Upgrade to React 19                               2d ago
 ─────────────────────────────────────────────────────────────────────────────
 enter jump · o open dir · r rename · d done · x hide · a all · / filter · p preview · q quit
```

It has two parts. Claude Code runs `cc-board hook` on session events, and the
hook writes a small status file per session under `~/.claude/board/sessions/`.
The TUI reads those status files and the transcripts in `~/.claude/projects/`.
It runs locally and never uses the network.

## Install

Requires Go 1.22+ and tmux.

```sh
go install github.com/dpbrackin/cc-board/cmd/cc-board@latest
```

Or from a checkout: `make install` (or `make build` for `./cc-board`).

### 1. Register the hooks

Merge this into `~/.claude/settings.json`. If you already have a `hooks` key, add
these entries to it.

```json
{
  "hooks": {
    "SessionStart":     [{ "hooks": [{ "type": "command", "command": "cc-board hook" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "cc-board hook" }] }],
    "PostToolUse":      [{ "matcher": "*", "hooks": [{ "type": "command", "command": "cc-board hook" }] }],
    "Notification":     [{ "hooks": [{ "type": "command", "command": "cc-board hook" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "cc-board hook" }] }],
    "SessionEnd":       [{ "hooks": [{ "type": "command", "command": "cc-board hook" }] }]
  }
}
```

If `cc-board` is not on the PATH that Claude Code sees, use the absolute path,
for example `~/go/bin/cc-board hook`.

The hook never prints anything and always exits 0. Errors are written to
`~/.claude/board/hook.log`.

### 2. Add the tmux popup binding

In `~/.tmux.conf`:

```
bind-key C display-popup -E -w 80% -h 80% cc-board
```

Then run `tmux source-file ~/.tmux.conf`. `prefix C` opens the board from any pane.

### 3. Let Claude report progress (optional)

Without this, the progress bar comes from Claude's own task list, so it only
appears for multi-step work. To have every session report progress itself,
add this to `~/.claude/CLAUDE.md`:

```markdown
## Progress reporting
For multi-step work, report progress with `cc-board progress <percent> <short note>`,
for example `cc-board progress 40 writing tests`. Report when you start, after each
meaningful step, and `cc-board progress 100 done` when finished. Keep notes under
60 characters.
```

And allow the command without a permission prompt each time, in the
`permissions` section of `~/.claude/settings.json`:

```json
{
  "permissions": {
    "allow": ["Bash(cc-board progress:*)"]
  }
}
```

Each report is one tool call, so it costs Claude a little time and context.
That is why the instruction asks for meaningful steps, not every action.

### 4. Restart running Claude Code sessions

Running sessions only pick up the hooks after a restart. Sessions that
started before the hooks were installed show as `closed`, using transcript data
only.

## Usage

| Command | Purpose |
| --- | --- |
| `cc-board` | Open the TUI |
| `cc-board hook` | Called by Claude Code hooks; reads event JSON on stdin |
| `cc-board list [-a]` | Print sessions as plain text (`-a` includes hidden and old closed ones) |
| `cc-board progress <N \| N% \| DONE/TOTAL> [note...]` | Report progress for the current session |
| `cc-board progress -clear` | Remove reported progress for the current session |
| `cc-board version` | Print version |

All commands accept `-config <path>`.

### Keys

| Key | Action |
| --- | --- |
| `enter` | Jump to the live pane, or resume the session in a new window (`claude --resume <id>`) |
| `o` | Open a plain shell window in the session's directory |
| `r` | Rename the task (enter saves, esc cancels, empty resets to the first prompt) |
| `d` | Toggle done (✓, sorts to the bottom of its group) |
| `x` | Toggle hidden |
| `a` | Show all, including hidden and old closed sessions |
| `/` | Filter by title or project (enter keeps the filter, esc clears it) |
| `p` | Toggle the preview pane with the last assistant message (`J`/`K` or `ctrl+d`/`ctrl+u` scroll it) |
| `j`/`k`, arrows | Move (`g`/`G` for top and bottom) |
| `q`, `esc` | Quit |

Statuses:

- **needs you**: Claude is waiting for a permission prompt or input.
- **working**: Claude is in the middle of a turn.
- **idle**: Claude finished its turn.
- **closed**: the session has ended, its pane is gone or now runs something
  else, or it has no status file.

### Progress

The bar shows progress the session reported with `cc-board progress`, and
the note after the title (`Add rate limiting · writing tests`). A session that
never reports falls back to its task list: completed tasks over total. If it has
neither, the column is blank.

`cc-board progress` works out which session is calling from `$TMUX_PANE`,
matched against the pane the hook recorded. Outside tmux it uses the session
whose directory contains the current directory (the deepest match wins). To
report for a specific session, pass `-session <id>` before the value:

```sh
cc-board progress 40 writing tests
cc-board progress 3/5 "wiring up the API"
cc-board progress -session 1f2e… 100 done
cc-board progress -clear
```

Reported progress stays until it is replaced or cleared.

A dim `done?` means progress is at 100% and the session is idle.

Outside tmux, jumping to a live pane is not possible. Resume and open still
work: the TUI exits and replaces itself with the resume command or `$SHELL`,
started in the session's directory.

## Configuration

The config file is optional: `~/.config/cc-board/config.json`. Every key has a
default.

```json
{
  "claude_dir": "~/.claude",
  "poll_interval": "2s",
  "hide_closed_after": "168h",
  "live_commands": ["claude", "node"],
  "resume_command": "claude --resume {id}"
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `claude_dir` | `$CLAUDE_CONFIG_DIR` or `~/.claude` | Where transcripts and the board folder live |
| `poll_interval` | `"2s"` | TUI refresh interval |
| `hide_closed_after` | `"168h"` | Hide closed sessions older than this (`"0s"` never hides them) |
| `live_commands` | `["claude", "node"]` | Pane commands that count as a running session |
| `resume_command` | `"claude --resume {id}"` | Command for resuming a closed session |

## Files

| Path | Written by | Contents |
| --- | --- | --- |
| `~/.claude/board/sessions/<id>.json` | hook | status, cwd, tmux pane, timestamps |
| `~/.claude/board/meta/<id>.json` | TUI | your label, done and hidden flags |
| `~/.claude/board/progress/<id>.json` | `cc-board progress` | reported percent, optional done/total, note, timestamp |
| `~/.claude/board/hook.log` | hook | errors only |

You can delete the board folder at any time. It is rebuilt as events arrive.

## Development

```sh
make build   # ./cc-board
make test    # go test ./...
make lint    # go vet + gofmt check
```

The transcript format is internal to Claude Code and changes between versions.
The parser skips lines and fields it does not recognize. The todo tool names
(`TodoWrite`, and the newer `TaskCreate`/`TaskUpdate`) are defined in one place,
in `internal/transcript/transcript.go`.
