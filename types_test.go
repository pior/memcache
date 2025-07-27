package memcache

import (
	"testing"
	"time"
)

func TestNewGetCommand(t *testing.T) {
	cmd := NewGetCommand("test_key")

	if cmd.Type != "mg" {
		t.Errorf("Expected type mg, got %s", cmd.Type)
	}

	if cmd.Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", cmd.Key)
	}

	if cmd.Flags["v"] != "" {
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

	if cmd.TTL != 3600 {
		t.Errorf("Expected TTL 3600, got %d", cmd.TTL)
	}
}

func TestNewSetCommandZeroTTL(t *testing.T) {
	cmd := NewSetCommand("test_key", []byte("value"), 0)

	if cmd.TTL != 0 {
		t.Errorf("Expected TTL 0, got %d", cmd.TTL)
	}
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

func TestCommandSetFlagNilFlags(t *testing.T) {
	cmd := &Command{
		Type:  "mg",
		Key:   "key",
		Flags: nil,
	}

	cmd.SetFlag("test", "value")

	if cmd.Flags == nil {
		t.Error("flags should be initialized")
	}

	value, exists := cmd.GetFlag("test")
	if !exists {
		t.Error("flag should exist")
	}

	if value != "value" {
		t.Errorf("Expected flag value 'value', got %s", value)
	}
}

func TestResponseSetFlag(t *testing.T) {
	resp := &Response{
		Status: "HD",
		Key:    "test_key",
	}

	resp.SetFlag("format", "json")

	value, exists := resp.GetFlag("format")
	if !exists {
		t.Error("flag should exist")
	}

	if value != "json" {
		t.Errorf("Expected flag value json, got %s", value)
	}
}

func TestResponseGetFlagNotExists(t *testing.T) {
	resp := &Response{
		Status: "HD",
		Key:    "test_key",
	}

	value, exists := resp.GetFlag("nonexistent")
	if exists {
		t.Error("flag should not exist")
	}

	if value != "" {
		t.Errorf("Expected empty value, got %s", value)
	}
}
