package protocol

import (
	"bytes"
	"sort"
	"strconv"
)

func CommandToProtocol(cmd *Command) []byte {
	var buf bytes.Buffer

	buf.WriteString(cmd.Type)

	if cmd.Type != CmdMetaNoOp {
		buf.WriteByte(' ')
		buf.WriteString(cmd.Key)
	}

	if cmd.Type == CmdMetaSet {
		buf.WriteByte(' ')
		buf.WriteString(strconv.Itoa(len(cmd.Value)))
	}

	// write flags after sorting them
	flags := cmd.Flags
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Type < flags[j].Type
	})
	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag.Type)
		if flag.Value != "" {
			buf.WriteString(flag.Value)
		}
	}

	if cmd.Opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(cmd.Opaque)
	}

	if cmd.Type == CmdMetaSet {
		buf.WriteString("\r\n")
		buf.Write(cmd.Value)
	}

	buf.WriteString("\r\n")
	return buf.Bytes()
}
