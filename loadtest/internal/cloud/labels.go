// Package cloud holds the orchestrator's GCP-facing logic: run identity,
// resource labels, geographic placement, and VM startup-script generation. The
// pure logic here is unit-tested; the live Compute/Storage calls live behind the
// Provisioner interface so a run can be planned (dry-run) without touching GCP.
package cloud

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// AppLabel tags every resource this tool creates, for cost reporting and reaping.
const AppLabel = "memcache-loadtest"

// NewRunID returns a sortable, unique run identifier like 20260613-060730-a1b2.
func NewRunID(now time.Time) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var suffix [4]byte
	for i := range suffix {
		suffix[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return fmt.Sprintf("%s-%s", now.Format("20060102-150405"), string(suffix[:]))
}

// Role distinguishes client and server VMs.
type Role string

const (
	RoleClient Role = "client"
	RoleServer Role = "server"
)

// Labels builds the GCE label set for a resource. created is unix seconds.
func Labels(runID string, role Role, profile, owner string, created int64, ttlHours int) map[string]string {
	return map[string]string{
		"app":       AppLabel,
		"run-id":    sanitizeLabel(runID),
		"role":      string(role),
		"profile":   sanitizeLabel(profile),
		"owner":     sanitizeLabel(owner),
		"created":   fmt.Sprintf("%d", created),
		"ttl-hours": fmt.Sprintf("%d", ttlHours),
	}
}

// sanitizeLabel coerces a string into a valid GCE label value: lowercase, only
// [a-z0-9_-], at most 63 chars. (Keys/values share these rules.)
func sanitizeLabel(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}
