package memcache

import (
	"context"
	"strconv"
	"time"

	"github.com/pior/memcache/protocol"
)

// NewGetCommand creates a new get command
func NewGetCommand(key string) *protocol.Command {
	cmd := protocol.NewCommand(protocol.CmdGet, key)
	cmd.Flags.Set(protocol.FlagValue, "")
	return cmd
}

// NewSetCommand creates a new set command
func NewSetCommand(key string, value []byte, ttl time.Duration) *protocol.Command {
	cmd := protocol.NewCommand(protocol.CmdSet, key)
	cmd.Value = value
	if ttl > 0 {
		cmd.Flags.Set(protocol.FlagSetTTL, strconv.Itoa(int(ttl.Seconds())))
	}
	return cmd
}

// NewDeleteCommand creates a new delete command
func NewDeleteCommand(key string) *protocol.Command {
	return protocol.NewCommand(protocol.CmdDelete, key)
}

// NewIncrementCommand creates a new increment command
func NewIncrementCommand(key string, delta int64) *protocol.Command {
	cmd := protocol.NewCommand(protocol.CmdArithmetic, key)
	cmd.Flags.Set(protocol.FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(protocol.FlagMode, protocol.ArithIncrement)
	return cmd
}

// NewDecrementCommand creates a new decrement command
func NewDecrementCommand(key string, delta int64) *protocol.Command {
	cmd := protocol.NewCommand(protocol.CmdArithmetic, key)
	cmd.Flags.Set(protocol.FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(protocol.FlagMode, protocol.ArithDecrement)
	return cmd
}

// NewDebugCommand creates a new debug command
func NewDebugCommand(key string) *protocol.Command {
	return protocol.NewCommand(protocol.CmdDebug, key)

}

// NewNoOpCommand creates a new no-op command
func NewNoOpCommand() *protocol.Command {
	return protocol.NewCommand(protocol.CmdNoOp, "")
}

// WaitAll waits for all command responses to be ready.
//
// This function blocks until all the provided commands have their responses available,
// or until the context is cancelled. It's useful when you've executed multiple commands
// using client.Do() and want to ensure all responses are ready before proceeding.
//
// Returns nil if all commands complete successfully, or the first error encountered
// (including context cancellation or timeout).
//
// Example usage:
//
//	commands := []*protocol.Command{
//		NewSetCommand("key1", []byte("value1"), time.Hour),
//		NewSetCommand("key2", []byte("value2"), time.Hour),
//	}
//
//	// Execute commands asynchronously
//	err := client.Do(ctx, commands...)
//	if err != nil {
//		return err
//	}
//
//	// Wait for all responses to be ready
//	err = WaitAll(ctx, commands...)
//	if err != nil {
//		return err
//	}
//
//	// Now all responses are guaranteed to be available
//	for _, cmd := range commands {
//		resp, _ := cmd.GetResponse(ctx)
//		// Process response...
//	}
func WaitAll(ctx context.Context, commands ...*protocol.Command) error {
	for _, cmd := range commands {
		err := cmd.Wait(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}
