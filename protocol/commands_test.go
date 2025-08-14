package protocol

import (
	"strconv"
	"time"
)

func newGetCommand(key string) *Command {
	return NewCommand(CmdMetaGet, key).
		SetFlag(FlagValue, "")

}

func newSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := NewCommand(CmdMetaSet, key).SetValue(value)
	if ttl > 0 {
		cmd.TTL = int(ttl.Seconds())
	}
	return cmd
}

func newDeleteCommand(key string) *Command {
	return NewCommand(CmdMetaDelete, key)
}

func newIncrementCommand(key string, delta int64) *Command {
	cmd := NewCommand(CmdMetaArithmetic, key).
		SetFlag(FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(FlagMode, ArithIncrement)
	return cmd
}

func newDecrementCommand(key string, delta int64) *Command {
	cmd := NewCommand(CmdMetaArithmetic, key).
		SetFlag(FlagDelta, strconv.FormatInt(delta, 10))
	cmd.Flags.Set(FlagMode, ArithDecrement)
	return cmd
}
