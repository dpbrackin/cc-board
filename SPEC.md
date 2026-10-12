# cc-board: Claude Code Session Board — Spec

## Overview

cc-board is a single Go binary that shows every Claude Code session on the machine in one TUI, with live status and todo progress, and jumps to the selected session with one key. It is opened as a tmux popup from any pane and works from any directory.

**Problem.** With several Claude Code sessions running in tmux, it is hard to remember which pane holds which task, which ones are waiting on you, and which are finished.

**Goals**

- List all sessions across all projects, grouped by project directory.
- Show live status per session: working, needs you, idle, or closed.
- Show progress as completed / total todos from the session's latest todo list.
- Enter on a session: switch to its live tmux pane, or resume it in a new tmux window in its directory if it has closed.
- Let the user rename a task, mark it done, and hide it.

**Non-goals (v1)**

- Starting new sessions, managing git worktrees, or editing transcripts.
- Multiplexers other than tmux, and Windows.
- Any network access. Everything is local files and the `tmux` CLI.

## Architecture

The system has two halves that share files: a hook that Claude Code runs on every event, and a TUI that reads those files plus the transcripts and drives tmux.

```
Claude Code session (tmux pane) --events--> cc-board hook --writes--> status files (~/.claude/board/sessions)
        |                                                                  |
      writes                                                             reads
        v                                                                  v
Transcripts (~/.claude/projects) ------------reads------------------> cc-board TUI <----> meta files (label, done, hidden)
                                                                          |
                                                                   switch / resume
                                                                          v
                                                         tmux (switch pane or new window) --> back to the session
```

The hook never talks to the TUI directly; the status files are the only interface, so either side can restart at any time. The TUI is the only writer of meta files, and only the hook writes status files.

## Project layout

The repo follows the standard Go layout: one binary under `cmd/`, all packages under `internal/`, test fixtures in `testdata/`.

```
cc-board/
├── cmd/
│   └── cc-board/
│       └── main.go          # subcommand dispatch: (none) = TUI, hook, list, version
├── internal/
│   ├── config/              # load ~/.config/cc-board/config.json, defaults
│   ├── hook/                # `cc-board hook`: parse event JSON from stdin, update status file
│   ├── store/               # status + meta file read/write, atomic writes
│   ├── transcript/          # parse ~/.claude/projects/**/*.jsonl, mtime cache
│   ├── session/             # Session model, merge of store + transcript, sorting
│   ├── tmux/                # pane checks, switch, new-window (behind a Runner interface)
│   └── tui/                 # Bubble Tea model, update, view, keys, styles
├── testdata/                # sample transcripts and hook payloads
├── go.mod
├── Makefile                 # build, test, lint, install
└── README.md
```

**Subcommands**

| Command | Purpose |
| --- | --- |
| `cc-board` | Open the TUI |
| `cc-board hook` | Called by Claude Code hooks; reads event JSON on stdin, writes the status file |
| `cc-board list` | Print sessions as plain text (debugging, scripting) |
| `cc-board version` | Print version |

**Dependencies.** Go 1.22+, `github.com/charmbracelet/bubbletea`, `bubbles` (list, textinput, viewport) and `lipgloss`. Flags use the standard `flag` package; no CLI framework.

## Data sources

A session's row merges three files keyed by `session_id`: a status file written by the hook, a meta file written by the TUI, and the Claude Code transcript.

### Status file (written by the hook)

Path: `~/.claude/board/sessions/<session_id>.json`

| Field | Type | Source |
| --- | --- | --- |
| `session_id` | string | hook stdin |
| `cwd` | string | hook stdin |
| `transcript_path` | string | hook stdin |
| `tmux_pane` | string, e.g. `%12` | `$TMUX_PANE` env, empty outside tmux |
| `status` | `working` · `needs_input` · `idle` · `ended` | derived from the event (see Hook command) |
| `last_event` | string | `hook_event_name` from stdin |
| `started_at` | RFC 3339 time | first event seen |
| `updated_at` | RFC 3339 time | every event |

### Meta file (written by the TUI)

Path: `~/.claude/board/meta/<session_id>.json`. Kept separate so hook writes never overwrite user edits.

| Field | Type | Meaning |
| --- | --- | --- |
| `label` | string | User-set task name |
| `done` | bool | User marked the task done |
| `hidden` | bool | Hidden from the default view |

### Transcript

Path: `~/.claude/projects/<encoded-path>/<session_id>.jsonl`, one JSON object per line. The parser reads:

- `cwd` from the entries, never from the encoded folder name (that encoding replaces `/` with `-` and is ambiguous).
- The first real user prompt as the default title, truncated to 60 characters. Skip meta entries such as slash-command wrappers.
- The latest todo list: the last assistant `tool_use` whose name is `TodoWrite`, counting `input.todos[].status == "completed"` against the total.
- The last `timestamp` as last activity, falling back to file mtime.
- The last assistant text block, for the preview pane.

The transcript format is internal to Claude Code and may change. The parser must skip unknown lines and fields without failing, and the todo tool name lives in one constant.

**Cache.** Results are cached in memory by `(path, size, mtime)`; a transcript is re-parsed only when it changes.

### Merge rules

1. Every transcript is a session. A status file with no transcript is ignored.
2. Title = meta `label`, else first prompt, else `(untitled)`.
3. Status comes from the status file. With no status file (sessions from before the hook was installed), status is `closed`.
4. A session is **live** only if its status is not `ended`, `tmux_pane` is set, and that pane exists and runs Claude (see Jump). Otherwise it displays as `closed`.
5. Progress = completed / total of the latest todo list; blank if there is none.
6. Done = meta `done`. If all todos are complete and status is `idle`, the row shows a dim "done?" hint.

## Hook command

`cc-board hook` is registered for six Claude Code events. It reads the event JSON from stdin, maps the event to a status, and rewrites the session's status file.

| Event | New status | Note |
| --- | --- | --- |
| `SessionStart` | `idle` | Sets `started_at` and records `cwd`, `tmux_pane` |
| `UserPromptSubmit` | `working` | |
| `PostToolUse` | `working` | Clears `needs_input` after a permission prompt is answered |
| `Notification` | `needs_input` | Ignored if current status is `idle`, so the idle reminder doesn't flip a finished turn |
| `Stop` | `idle` | Claude finished its turn |
| `SessionEnd` | `ended` | |

**Behavior rules**

- Always exit 0 and never write to stdout. Stdout from some hooks (`SessionStart`, `UserPromptSubmit`) is added to Claude's context. Errors go to `~/.claude/board/hook.log`.
- Finish in under 50 ms: no transcript parsing, no tmux calls.
- Write atomically: write a temp file in the same directory, then `os.Rename`.
- Create `~/.claude/board/sessions/` if missing.
- Ignore unknown fields and unknown events.

**settings.json** (`~/.claude/settings.json`, merged into any existing `hooks` key):

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

If `cc-board` is not on the PATH Claude Code sees, use the absolute path, e.g. `~/go/bin/cc-board hook`.

## Reported progress

Added after v1. A session can report its own progress, which overrides the todo ratio.

- `cc-board progress [-session id] <N | N% | DONE/TOTAL> [note...]` writes `~/.claude/board/progress/<session_id>.json` with `percent`, optional `done`/`total`, `note` (cut to 80 characters) and `updated_at`. `-clear` removes the file.
- The calling session is found from `$TMUX_PANE`, matched to `tmux_pane` in the status files (most recent non-ended match wins). Outside tmux it falls back to the non-ended session whose `cwd` contains the current directory, deepest match first. `-session` overrides both.
- Merge: reported progress, when present, sets the bar and label (`60%` or `3/5`) and appends ` · <note>` to the title; otherwise the todo ratio is used. A report also counts as activity for sorting. `done?` shows when progress is 100% and the session is idle.
- Claude is asked to report via a CLAUDE.md instruction, with `Bash(cc-board progress:*)` in `permissions.allow` so reports don't prompt.

## TUI

The TUI is one Bubble Tea program: a header with counts, a list of sessions grouped by project, an optional preview pane, and a one-line help footer.

```
 cc-board                         1 needs you · 2 working · 4 idle · 6 closed
 ─────────────────────────────────────────────────────────────────────────────
 api  ~/projects/api
 ● needs you   Add rate limiting to /login          ███░░  3/5    just now
 ◐ working     Fix flaky integration tests          █░░░░  1/4    2m ago
 web  ~/projects/web
 ○ idle        Migrate settings page to new forms   █████  6/6    18m ago  done?
 ✓ closed      Upgrade to React 19                               2d ago
 ─────────────────────────────────────────────────────────────────────────────
 enter jump · o open dir · r rename · d done · x hide · / filter · p preview · q quit
```

**Rows**

- Grouped by project: group header = basename of `cwd`, with the full path dimmed.
- Columns: status badge, title, progress bar with `done/total`, last activity as relative time, `done?` hint.
- Sort groups by most recent activity. Within a group: `needs_input`, `working`, `idle`, `closed`, then by `updated_at` descending.
- Done sessions show a ✓ badge and sort to the bottom of their group.
- By default hide sessions that are hidden, or closed for more than 7 days (configurable).

**Keybindings**

| Key | Action |
| --- | --- |
| `enter` | Jump to the live pane, or resume in a new window (see Jump) |
| `o` | Open a plain shell window in the session's directory |
| `r` | Rename: inline text input, saved to meta `label` |
| `d` | Toggle done |
| `x` | Toggle hidden |
| `a` | Show all, including hidden and old closed sessions |
| `/` | Filter by title or project (bubbles list filter) |
| `p` | Toggle preview pane: last assistant message, scrollable |
| `j` / `k`, arrows | Move |
| `q`, `esc` | Quit (closes the popup) |

**Refresh.** Poll every 2 s. Re-read status files each tick (they are small); re-parse a transcript only when its size or mtime changed; check pane liveness for sessions whose status is not `ended`. All I/O runs in `tea.Cmd`s, never in `Update`.

**Styling.** lipgloss with the terminal's default colors: yellow for needs you, blue for working, default for idle, dim for closed. No custom theme in v1.

## Jump, resume and tmux

Enter on a session switches to its live pane when one exists, otherwise opens a new tmux window in the session's directory running `claude --resume <id>`. The TUI then exits, which closes the popup.

**Liveness check** (`internal/tmux`)

```
tmux display-message -p -t <pane> '#{pane_current_command}'
```

The pane is live if the command succeeds and prints a name in `live_commands` (default `claude`, `node`). A non-zero exit means the pane is gone. Pane IDs are reused after a pane closes, so a pane running something else is treated as closed.

**On `enter`**

1. Live pane → `tmux select-window -t <pane> \; select-pane -t <pane> \; switch-client -t <pane>`.
2. Not live and `cwd` exists → `tmux new-window -c <cwd> -n <short-title> 'claude --resume <id>'`. The new window opens in the client's current session.
3. `cwd` no longer exists → show an error in the footer and do nothing.

**On `o`** → `tmux new-window -c <cwd>`.

**Outside tmux** (`$TMUX` unset): live jumps are not possible, so the footer says so. Resume and open still work: the TUI exits, changes to `cwd`, and `syscall.Exec`s the resume command or `$SHELL`.

**tmux binding** (`~/.tmux.conf`):

```
bind-key C display-popup -E -w 80% -h 80% cc-board
```

All tmux calls go through a small `Runner` interface (`Run(args ...string) (string, error)`) so tests can use a fake.

## Configuration

Optional file `~/.config/cc-board/config.json`; every key has a default.

| Key | Default | Meaning |
| --- | --- | --- |
| `claude_dir` | `$CLAUDE_CONFIG_DIR` or `~/.claude` | Where transcripts and the board folder live |
| `poll_interval` | `"2s"` | TUI refresh interval |
| `hide_closed_after` | `"168h"` | Hide closed sessions older than this (7 days) |
| `live_commands` | `["claude", "node"]` | Pane commands that count as a running session |
| `resume_command` | `"claude --resume {id}"` | Command for resuming a closed session |

## Install

1. `go install github.com/<you>/cc-board/cmd/cc-board@latest`
2. Add the hooks block to `~/.claude/settings.json`.
3. Add the popup binding to `~/.tmux.conf`, then `tmux source-file ~/.tmux.conf`.
4. Restart running Claude Code sessions so they pick up the hooks.

## Edge cases

- **Sessions started before the hook was installed** show as `closed`, using transcript data only.
- **Pane reuse** is handled by the liveness check on `pane_current_command`.
- **Session killed without `SessionEnd`** (pane closed, crash): the status file still says `working`, but the pane check marks it closed.
- **Concurrent hook writes** for the same session are serialized by atomic rename; last write wins.
- **Large transcripts** (tens of MB): parse with `bufio.Scanner` and a 10 MB line buffer, and cache by size and mtime.
- **Deleted project directories**: show the row with the path struck through; `enter` and `o` show an error.

## Testing

- `internal/transcript`: table-driven tests over fixture files in `testdata/` (no todos, several todo updates, malformed lines, meta-only first prompt).
- `internal/hook`: feed sample payloads on stdin, assert the status file and that stdout stays empty.
- `internal/session`: merge and sort rules, table-driven.
- `internal/tmux`: fake `Runner` to assert the exact commands issued for live, closed and missing-cwd cases.
- `internal/tui`: test `Update` with key messages; no snapshot tests in v1.

## Milestones

1. **M1, data:** `hook`, `store`, `transcript`, `session`, and `cc-board list` printing merged sessions.
2. **M2, TUI:** grouped list, status badges, progress, polling refresh.
3. **M3, navigation:** liveness check, jump, resume, open dir, popup binding.
4. **M4, polish:** rename, done, hide, filter, preview pane, config file.

## Open questions

- Does `claude --resume` keep the same session ID or create a new one? If new, the resumed session shows as a second row; link them via the `SessionStart` payload if it carries the original ID.
- Confirm the current transcript field names and the todo tool name against a real `.jsonl` before building M1.
