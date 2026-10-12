// Package config loads the optional cc-board configuration file and fills in
// defaults for every key.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Config is the resolved cc-board configuration.
type Config struct {
	// ClaudeDir is where transcripts (projects/) and the board folder live.
	ClaudeDir string
	// PollInterval is the TUI refresh interval.
	PollInterval time.Duration
	// HideClosedAfter hides closed sessions whose last activity is older than this.
	HideClosedAfter time.Duration
	// LiveCommands are pane commands that count as a running session.
	LiveCommands []string
	// ResumeCommand resumes a closed session; {id} is replaced by the session ID.
	ResumeCommand string
}

// fileConfig mirrors the JSON file. Pointers distinguish "unset" from zero.
type fileConfig struct {
	ClaudeDir       *string  `json:"claude_dir"`
	PollInterval    *string  `json:"poll_interval"`
	HideClosedAfter *string  `json:"hide_closed_after"`
	LiveCommands    []string `json:"live_commands"`
	ResumeCommand   *string  `json:"resume_command"`
}

// Defaults.
const (
	DefaultPollInterval    = 2 * time.Second
	DefaultHideClosedAfter = 168 * time.Hour
	DefaultResumeCommand   = "claude --resume {id}"
)

// DefaultLiveCommands returns the default live_commands value.
func DefaultLiveCommands() []string { return []string{"claude", "node"} }

// DefaultPath returns ~/.config/cc-board/config.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cc-board", "config.json")
}

// DefaultClaudeDir returns $CLAUDE_CONFIG_DIR, or ~/.claude.
func DefaultClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		ClaudeDir:       DefaultClaudeDir(),
		PollInterval:    DefaultPollInterval,
		HideClosedAfter: DefaultHideClosedAfter,
		LiveCommands:    DefaultLiveCommands(),
		ResumeCommand:   DefaultResumeCommand,
	}
}

// Load reads the config file at path. A missing file is not an error and
// yields the defaults. An empty path means DefaultPath().
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	return parse(cfg, data)
}

func parse(cfg Config, data []byte) (Config, error) {
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if fc.ClaudeDir != nil && *fc.ClaudeDir != "" {
		cfg.ClaudeDir = expandHome(*fc.ClaudeDir)
	}
	if fc.PollInterval != nil {
		d, err := time.ParseDuration(*fc.PollInterval)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("config: invalid poll_interval %q", *fc.PollInterval)
		}
		cfg.PollInterval = d
	}
	if fc.HideClosedAfter != nil {
		d, err := time.ParseDuration(*fc.HideClosedAfter)
		if err != nil || d < 0 {
			return cfg, fmt.Errorf("config: invalid hide_closed_after %q", *fc.HideClosedAfter)
		}
		cfg.HideClosedAfter = d
	}
	if fc.LiveCommands != nil {
		cfg.LiveCommands = fc.LiveCommands
	}
	if fc.ResumeCommand != nil && *fc.ResumeCommand != "" {
		cfg.ResumeCommand = *fc.ResumeCommand
	}
	return cfg, nil
}

// BoardDir is <claude_dir>/board.
func (c Config) BoardDir() string { return filepath.Join(c.ClaudeDir, "board") }

// ProjectsDir is <claude_dir>/projects.
func (c Config) ProjectsDir() string { return filepath.Join(c.ClaudeDir, "projects") }

func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
