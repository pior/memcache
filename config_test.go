package memcache

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantSub string // substring an emitted warning must contain; "" = no warnings
	}{
		{"zero value is clean", Config{}, ""},
		{
			"sensible lifetimes are clean",
			Config{MaxConnLifetime: time.Minute, MaxConnIdleTime: 30 * time.Second},
			"",
		},
		{
			"idle >= lifetime is flagged",
			Config{MaxConnLifetime: 30 * time.Second, MaxConnIdleTime: time.Minute},
			"idle limit can never fire",
		},
		{
			"sub-second lifetime is flagged",
			Config{MaxConnLifetime: 500 * time.Millisecond},
			"under 1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := tt.cfg.Validate()
			if tt.wantSub == "" {
				assert.Empty(t, warnings)
				return
			}
			assert.Contains(t, strings.Join(warnings, "\n"), tt.wantSub)
		})
	}
}
