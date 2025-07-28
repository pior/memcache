package memcache

import (
	"context"
	"testing"
)

func TestSimplePipelineOpaqueMatching(t *testing.T) {
	// Test the core opaque matching logic by simulating the readResponsesAsync behavior

	// Create commands
	cmd1 := NewGetCommand("key1")
	cmd2 := NewGetCommand("key2")
	commands := []*Command{cmd1, cmd2}

	// Ensure opaques are set (simulating commandToProtocol behavior)
	for _, cmd := range commands {
		if cmd.opaque == "" {
			cmd.opaque = generateOpaque()
		}
	}

	t.Logf("cmd1 opaque: %s", cmd1.opaque)
	t.Logf("cmd2 opaque: %s", cmd2.opaque)

	// Create map like in readResponsesAsync
	opaqueToCommand := make(map[string]*Command)
	processedOpaques := make(map[string]bool)
	for _, cmd := range commands {
		opaqueToCommand[cmd.opaque] = cmd
	}

	// Simulate responses coming in reverse order
	responses := []*metaResponse{
		{Status: "HD", Opaque: cmd2.opaque}, // cmd2 response first
		{Status: "HD", Opaque: cmd1.opaque}, // cmd1 response second
	}

	// Process responses like readResponsesAsync would
	for _, resp := range responses {
		if cmd, exists := opaqueToCommand[resp.Opaque]; exists && !processedOpaques[resp.Opaque] {
			cmd.setResponse(protocolToResponse(resp, cmd.Key))
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
