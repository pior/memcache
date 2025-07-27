package memcache

import (
	"testing"
	"time"
)

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
