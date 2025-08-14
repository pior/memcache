package memcache

import (
	"context"
	"testing"

	"github.com/pior/memcache/protocol"
)

func TestSimplePipelineOpaqueMatching(t *testing.T) {
	// Test the core opaque matching logic by simulating the readResponsesAsync behavior

	// Create commands
	cmd1 := NewGetCommand("key1")
	cmd2 := NewGetCommand("key2")
	commands := []*protocol.Command{cmd1, cmd2}

	// Ensure opaques are set (simulating commandToProtocol behavior)
	for _, cmd := range commands {
		protocol.SetRandomOpaque(cmd)
	}

	t.Logf("cmd1 opaque: %s", cmd1.Opaque)
	t.Logf("cmd2 opaque: %s", cmd2.Opaque)

	// Create map like in readResponsesAsync
	opaqueToCommand := make(map[string]*protocol.Command)
	processedOpaques := make(map[string]bool)
	for _, cmd := range commands {
		opaqueToCommand[cmd.Opaque] = cmd
	}

	// Simulate responses coming in reverse order
	responses := []*protocol.MetaResponse{
		{Status: "HD", Opaque: cmd2.Opaque}, // cmd2 response first
		{Status: "HD", Opaque: cmd1.Opaque}, // cmd1 response second
	}

	// Process responses like readResponsesAsync would
	for _, resp := range responses {
		if cmd, exists := opaqueToCommand[resp.Opaque]; exists && !processedOpaques[resp.Opaque] {
			cmd.SetResponse(protocol.ProtocolToResponse(resp, cmd.Key))
			processedOpaques[resp.Opaque] = true
		} else {
			t.Errorf("Failed to match response opaque %s", resp.Opaque)
		}
	}

	// Check that both commands got their responses
	ctx := context.Background()

	resp1, err := cmd1.GetResponse(ctx)
	if err != nil {
		t.Errorf("cmd1.GetResponse() error = %v", err)
	} else if resp1.Status != "HD" {
		t.Errorf("cmd1 expected status HD, got %s", resp1.Status)
	}

	resp2, err := cmd2.GetResponse(ctx)
	if err != nil {
		t.Errorf("cmd2.GetResponse() error = %v", err)
	} else if resp2.Status != "HD" {
		t.Errorf("cmd2 expected status HD, got %s", resp2.Status)
	}
}
