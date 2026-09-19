package meta

import (
	"errors"
	"strings"
	"testing"
)

var allStatuses = []StatusType{StatusHD, StatusVA, StatusEN, StatusNF, StatusNS, StatusEX, StatusMN, StatusME, StatusOK}

// acceptedStatuses returns the statuses ValidateResponse accepts for req, as a
// space-separated string, so a wrong table shows up as a readable diff.
func acceptedStatuses(req *Request, respErr error) string {
	var accepted []string
	for _, status := range allStatuses {
		if ValidateResponse(req, &Response{Status: status, Error: respErr}) == nil {
			accepted = append(accepted, string(status))
		}
	}
	return strings.Join(accepted, " ")
}

func TestValidateResponse(t *testing.T) {
	const all = "HD VA EN NF NS EX MN ME OK"

	tests := []struct {
		name string
		req  *Request
		want string
	}{
		{"mg with v", NewRequest(CmdGet, "k", nil).AddReturnValue(), "VA EN"},
		{"mg with v among other flags", NewRequest(CmdGet, "k", nil).AddReturnCAS().AddReturnValue(), "VA EN"},
		{"mg without v", NewRequest(CmdGet, "k", nil).AddReturnCAS(), "HD EN"},
		{"mg quiet with v", NewRequest(CmdGet, "k", nil).AddReturnValue().AddQuiet(), "VA EN"},
		{"ms", NewRequest(CmdSet, "k", []byte("v")), "HD NF NS EX"},
		{"md", NewRequest(CmdDelete, "k", nil), "HD NF NS EX"},
		{"ma with v", NewRequest(CmdArithmetic, "k", nil).AddReturnValue(), "VA NF NS EX"},
		{"ma without v", NewRequest(CmdArithmetic, "k", nil), "HD NF NS EX"},
		{"me", NewRequest(CmdDebug, "k", nil), "EN ME"},
		{"mn", NewRequest(CmdNoOp, "", nil), "MN"},
		{"flush_all", NewRequest(CmdFlushAll, "", nil), "OK"},
		{"custom command", NewRequest(CmdType("mx"), "k", nil), all},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acceptedStatuses(tt.req, nil); got != tt.want {
				t.Errorf("accepted statuses = %q, want %q", got, tt.want)
			}
		})

		t.Run(tt.name+" with protocol error", func(t *testing.T) {
			// ERROR, CLIENT_ERROR and SERVER_ERROR can follow any command.
			if got := acceptedStatuses(tt.req, &ServerError{Message: "out of memory"}); got != all {
				t.Errorf("accepted statuses = %q, want %q", got, all)
			}
		})
	}

	t.Run("rejection closes the connection", func(t *testing.T) {
		err := ValidateResponse(NewRequest(CmdGet, "k", nil).AddReturnValue(), &Response{Status: StatusHD})

		if _, ok := errors.AsType[*ParseError](err); !ok {
			t.Fatalf("error = %v (%T), want *ParseError", err, err)
		}
		if !ShouldCloseConnection(err) {
			t.Error("ShouldCloseConnection = false, want true")
		}
		if got, want := err.Error(), "parse error: unexpected HD reply to mg"; got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
}
