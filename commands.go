package memcache

import (
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
