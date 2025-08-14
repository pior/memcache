package memcache

import (
	"context"
	"testing"

	"github.com/pior/memcache/protocol"
)

func TestBasicOpaqueMatching(t *testing.T) {
	// Create two commands and set their opaques manually for testing
	cmd1 := NewGetCommand("key1")
	cmd1.Opaque = "opaque1"

	cmd2 := NewGetCommand("key2")
	cmd2.Opaque = "opaque2"

	commands := []*protocol.Command{cmd1, cmd2}

	// Create mock responses
	resp1 := &protocol.MetaResponse{Status: "HD", Opaque: "opaque1"}
	resp2 := &protocol.MetaResponse{Status: "HD", Opaque: "opaque2"}

	// Test opaque to command mapping
	opaqueToCommand := make(map[string]*protocol.Command)
	for _, cmd := range commands {
		opaqueToCommand[cmd.Opaque] = cmd
	}

	// Simulate processing responses in reverse order (like our pipelining test)
	responses := []*protocol.MetaResponse{resp2, resp1} // resp2 first, then resp1

	for _, resp := range responses {
		if cmd, exists := opaqueToCommand[resp.Opaque]; exists {
			cmd.SetResponse(protocol.ProtocolToResponse(resp, cmd.Key))
		} else {
			t.Errorf("No command found for opaque %s", resp.Opaque)
		}
	}

	// Check that both commands got their responses
	ctx := context.Background()

	result1, err := cmd1.GetResponse(ctx)
	if err != nil {
		t.Errorf("cmd1.GetResponse() error = %v", err)
	} else if result1.Status != "HD" {
		t.Errorf("cmd1 expected status HD, got %s", result1.Status)
	}

	result2, err := cmd2.GetResponse(ctx)
	if err != nil {
		t.Errorf("cmd2.GetResponse() error = %v", err)
	} else if result2.Status != "HD" {
		t.Errorf("cmd2 expected status HD, got %s", result2.Status)
	}
}
