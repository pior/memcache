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

	if cmd.Opaque == "" {
		t.Error("Opaque should be generated")
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

	if cmd.Opaque == "" {
		t.Error("Opaque should be generated")
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

	if cmd.Opaque == "" {
		t.Error("Opaque should be generated")
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

func TestNewItem(t *testing.T) {
	key := "test_key"
	value := []byte("test_value")

	item := NewItem(key, value)

	if item.Key != key {
		t.Errorf("expected key %s, got %s", key, item.Key)
	}

	if string(item.Value) != string(value) {
		t.Errorf("expected value %s, got %s", value, item.Value)
	}

	if item.Flags == nil {
		t.Error("flags should be initialized")
	}

	if item.Expiration != 0 {
		t.Errorf("expected expiration 0, got %d", item.Expiration)
	}
}

func TestItemSetTTL(t *testing.T) {
	item := NewItem("key", []byte("value"))

	ttl := 30 * time.Second
	item.SetTTL(ttl)

	if item.Expiration != 30 {
		t.Errorf("expected expiration 30, got %d", item.Expiration)
	}
}

func TestItemSetFlag(t *testing.T) {
	item := NewItem("key", []byte("value"))

	item.SetFlag("format", "json")

	value, exists := item.GetFlag("format")
	if !exists {
		t.Error("flag should exist")
	}

	if value != "json" {
		t.Errorf("expected flag value json, got %s", value)
	}
}

func TestItemGetFlagNotExists(t *testing.T) {
	item := NewItem("key", []byte("value"))

	value, exists := item.GetFlag("nonexistent")
	if exists {
		t.Error("flag should not exist")
	}

	if value != "" {
		t.Errorf("expected empty value, got %s", value)
	}
}

func TestItemSetFlagNilFlags(t *testing.T) {
	item := &Item{
		Key:   "key",
		Value: []byte("value"),
		Flags: nil, // Explicitly set to nil
	}

	item.SetFlag("test", "value")

	if item.Flags == nil {
		t.Error("flags should be initialized")
	}

	value, exists := item.GetFlag("test")
	if !exists {
		t.Error("flag should exist")
	}

	if value != "value" {
		t.Errorf("expected flag value 'value', got %s", value)
	}
}

func TestItemGetFlagNilFlags(t *testing.T) {
	item := &Item{
		Key:   "key",
		Value: []byte("value"),
		Flags: nil, // Explicitly set to nil
	}

	value, exists := item.GetFlag("test")
	if exists {
		t.Error("flag should not exist when flags is nil")
	}

	if value != "" {
		t.Errorf("expected empty value, got %s", value)
	}
}

func TestGenerateOpaque(t *testing.T) {
	opaque1 := GenerateOpaque()
	opaque2 := GenerateOpaque()

	if opaque1 == opaque2 {
		t.Error("generated opaques should be different")
	}

	if len(opaque1) != 8 { // 4 bytes = 8 hex chars
		t.Errorf("expected opaque length 8, got %d", len(opaque1))
	}

	if len(opaque2) != 8 {
		t.Errorf("expected opaque length 8, got %d", len(opaque2))
	}

	// Test that it's valid hex
	for _, char := range opaque1 {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			t.Errorf("opaque contains invalid hex character: %c", char)
		}
	}
}
