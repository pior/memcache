package profile

import (
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	p, err := Lookup("top-perf")
	if err != nil {
		t.Fatalf("Lookup(top-perf): %v", err)
	}
	if p.Intensity != Saturation || p.Workers != 64 {
		t.Errorf("top-perf = %+v", p)
	}
	if _, err := Lookup("nope"); err == nil {
		t.Error("Lookup(nope) should error")
	}
}

func TestClientConfig(t *testing.T) {
	p, _ := Lookup("top-perf")
	cfg := p.ClientConfig()
	if cfg.MaxSize != 16 || cfg.Timeout != time.Second {
		t.Errorf("client config = %+v", cfg)
	}
}

func TestStressTimeConstants(t *testing.T) {
	s := efficiency.WithStressTimeConstants()
	if s.MaxConnLifetime != 100*time.Millisecond || s.HealthCheck != 20*time.Millisecond {
		t.Errorf("stress constants not applied: %+v", s)
	}
	// original preset must be untouched (value receiver)
	if efficiency.MaxConnLifetime != 0 {
		t.Error("preset mutated")
	}
}

func TestEgressCap(t *testing.T) {
	cases := map[string]float64{
		"c3-highcpu-8":  16,
		"e2-standard-4": 8,
		"n2-highmem-32": 32, // capped at ceiling
		"e2-small":      2,  // shared-core -> 1 vcpu -> 2 Gbps
		"bogus":         0,
	}
	for mt, want := range cases {
		if got := EgressCapGbps(mt); got != want {
			t.Errorf("EgressCapGbps(%q) = %g, want %g", mt, got, want)
		}
	}
}
