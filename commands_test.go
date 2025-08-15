package memcache

import (
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
)

func TestNewGetCommand(t *testing.T) {
	cmd := NewGetCommand("test_key")

	if cmd.Type != "mg" {
		t.Errorf("Expected type mg, got %s", cmd.Type)
	}

	if cmd.Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", cmd.Key)
	}

	value, exists := cmd.GetFlag("v")
	if !exists || value != "" {
		t.Error("Get command should request value flag")
	}
}

func TestNewSetCommand(t *testing.T) {
	value := []byte("test_value")
	ttl := time.Hour

	cmd := NewSetCommand("test_key", value, ttl)

	if cmd.Type != "ms" {
		t.Errorf("Expected type ms, got %s", cmd.Type)
	}

	if cmd.Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", cmd.Key)
	}

	if string(cmd.Value) != string(value) {
		t.Errorf("Expected value %s, got %s", value, cmd.Value)
	}

	assertFlag(t, cmd, protocol.FlagSetTTL, "3600")
}

func TestNewSetCommandZeroTTL(t *testing.T) {
	cmd := NewSetCommand("test_key", []byte("value"), 0)

	assertFlagAbsent(t, cmd, protocol.FlagSetTTL)
}

func TestNewDeleteCommand(t *testing.T) {
	cmd := NewDeleteCommand("test_key")

	if cmd.Type != "md" {
		t.Errorf("Expected type md, got %s", cmd.Type)
	}

	if cmd.Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", cmd.Key)
	}
}

func TestCommandSetFlag(t *testing.T) {
	cmd := NewGetCommand("test_key")
	cmd.SetFlag("format", "json")

	value, exists := cmd.GetFlag("format")
	if !exists {
		t.Error("flag should exist")
	}

	if value != "json" {
		t.Errorf("Expected flag value json, got %s", value)
	}
}

func TestCommandGetFlagNotExists(t *testing.T) {
	cmd := NewGetCommand("test_key")

	value, exists := cmd.GetFlag("nonexistent")
	if exists {
		t.Error("flag should not exist")
	}

	if value != "" {
		t.Errorf("Expected empty value, got %s", value)
	}
}

func assertFlag(t testing.TB, cmd *protocol.Command, flag, want string) {
	got, found := cmd.Flags.Get(flag)
	if !found {
		t.Errorf("Expected flag %v", flag)
	}
	if got != want {
		t.Errorf("Expected flag %q to be %q, got %q", flag, want, got)
	}
}

func assertFlagAbsent(t testing.TB, cmd *protocol.Command, flag string) {
	got, found := cmd.Flags.Get(flag)
	if found {
		t.Errorf("Expected flag %q to be absent, got %q", flag, got)
	}
}
