package protocol

import (
	"strconv"
	"time"
)

func newGetCommand(key string) *Command {
	cmd := NewCommand(CmdGet, key)
	cmd.Flags.Set(FlagValue, "")
	return cmd
}

func newSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := NewCommand(CmdSet, key)
	cmd.Value = value
	if ttl > 0 {
		cmd.Flags.Set(FlagSetTTL, strconv.Itoa(int(ttl.Seconds())))
	}
	return cmd
}

func newDeleteCommand(key string) *Command {
	return NewCommand(CmdDelete, key)
}

func newIncrementCommand(key string, delta int64) *Command {
	cmd := NewCommand(CmdArithmetic, key)
	cmd.Flags.Set(FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(FlagMode, ArithIncrement)
	return cmd
}

func newDecrementCommand(key string, delta int64) *Command {
	cmd := NewCommand(CmdArithmetic, key)
	cmd.Flags.Set(FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(FlagMode, ArithDecrement)
	return cmd
}

func newDebugCommand(key string) *Command {
	return NewCommand(CmdDebug, key)
}

func newNoOpCommand() *Command {
	return NewCommand(CmdNoOp, "")
}
