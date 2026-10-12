package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-x")
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClaudeDir != "/tmp/claude-x" {
		t.Errorf("ClaudeDir = %q", cfg.ClaudeDir)
	}
	if cfg.PollInterval != 2*time.Second || cfg.HideClosedAfter != 168*time.Hour {
		t.Errorf("durations = %v %v", cfg.PollInterval, cfg.HideClosedAfter)
	}
	if !reflect.DeepEqual(cfg.LiveCommands, []string{"claude", "node"}) {
		t.Errorf("LiveCommands = %v", cfg.LiveCommands)
	}
	if cfg.ResumeCommand != "claude --resume {id}" {
		t.Errorf("ResumeCommand = %q", cfg.ResumeCommand)
	}
}

func TestLoadOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	data := `{"claude_dir":"/x","poll_interval":"500ms","hide_closed_after":"24h","live_commands":["claude"],"resume_command":"c -r {id}","unknown":1}`
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{ClaudeDir: "/x", PollInterval: 500 * time.Millisecond, HideClosedAfter: 24 * time.Hour, LiveCommands: []string{"claude"}, ResumeCommand: "c -r {id}"}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v want %+v", cfg, want)
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"poll_interval":"soon"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error")
	}
}
