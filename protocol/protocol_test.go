package protocol

import (
	"testing"
	"time"
)

func TestCommandToProtocol(t *testing.T) {
	tests := []struct {
		name string
		cmd  *Command
		want string
	}{
		{
			name: "get command",
			cmd:  newGetCommand("testkey"),
			want: "mg testkey v\r\n",
		},
		{
			name: "set command",
			cmd:  newSetCommand("testkey", []byte("value"), 0),
			want: "ms testkey 5\r\nvalue\r\n",
		},
		{
			name: "set command with TTL",
			cmd:  newSetCommand("testkey", []byte("value"), 60*time.Second),
			want: "ms testkey 5 T60\r\nvalue\r\n",
		},
		{
			name: "delete command",
			cmd:  newDeleteCommand("testkey"),
			want: "md testkey\r\n",
		},
		{
			name: "Meta Arithmetic",
			cmd:  newIncrementCommand("counter", 5),
			want: "ma counter D5 MI\r\n",
		},
		{
			name: "Meta Debug",
			cmd:  newDebugCommand("debug-key"),
			want: "me debug-key\r\n",
		},
		{
			name: "Meta NoOp",
			cmd:  newNoOpCommand(),
			want: "mn\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CommandToProtocol(tt.cmd)
			if result == nil {
				t.Error("commandToProtocol returned nil")
				return
			}
			assertEqualString(t, tt.want, string(result))
		})
	}
}
