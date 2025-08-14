package protocol

import (
	"testing"
)

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
