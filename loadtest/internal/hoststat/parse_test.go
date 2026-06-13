package hoststat

import (
	"testing"
	"time"
)

const (
	fixStat = "cpu  100 0 50 1000 20 0 5 0 0 0\n" +
		"cpu0 50 0 25 500 10 0 2 0 0 0\n" +
		"intr 12345\n"

	fixLoadavg = "0.50 0.40 0.30 1/234 5678\n"

	fixMeminfo = "MemTotal:       16384000 kB\n" +
		"MemFree:         1000000 kB\n" +
		"MemAvailable:    8000000 kB\n"

	fixNetDev = "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
		"    lo:  123 1 0 0 0 0 0 0 123 1 0 0 0 0 0 0\n" +
		"  eth0: 1000 10 0 2 0 0 0 0 2000 20 0 3 0 0 0 0\n"

	fixSnmp = "Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors\n" +
		"Tcp: 1 200 120000 -1 100 50 5 3 10 10000 9000 42 0 20 0\n"

	fixPSI = "some avg10=1.50 avg60=0.80 avg300=0.20 total=12345\n" +
		"full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
)

func TestParseProcStat(t *testing.T) {
	ct, ok := parseProcStat(fixStat)
	if !ok {
		t.Fatal("parseProcStat not ok")
	}
	if ct.total != 1175 || ct.idle != 1020 {
		t.Errorf("cpuTimes = %+v, want {total:1175 idle:1020}", ct)
	}
}

func TestParseLoadavg(t *testing.T) {
	la, ok := parseLoadavg(fixLoadavg)
	if !ok || la != [3]float64{0.50, 0.40, 0.30} {
		t.Errorf("loadavg = %v ok=%v", la, ok)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := parseMeminfo(fixMeminfo)
	if !ok || total != 16384000 || avail != 8000000 {
		t.Errorf("meminfo = %d/%d ok=%v", total, avail, ok)
	}
}

func TestParseNetDev(t *testing.T) {
	m := parseNetDev(fixNetDev)
	if _, ok := m["lo"]; ok {
		t.Error("loopback should be excluded")
	}
	eth0, ok := m["eth0"]
	if !ok {
		t.Fatal("eth0 missing")
	}
	want := netCounters{rxBytes: 1000, rxDrop: 2, txBytes: 2000, txDrop: 3}
	if eth0 != want {
		t.Errorf("eth0 = %+v, want %+v", eth0, want)
	}
}

func TestParseTCPRetrans(t *testing.T) {
	v, ok := parseTCPRetrans(fixSnmp)
	if !ok || v != 42 {
		t.Errorf("retrans = %d ok=%v, want 42", v, ok)
	}
}

func TestParsePSISome(t *testing.T) {
	v, ok := parsePSISome(fixPSI)
	if !ok || v != 0.015 {
		t.Errorf("psi some = %v ok=%v, want 0.015", v, ok)
	}
}

func TestComputeSampleRates(t *testing.T) {
	t0 := time.Now()
	prev := rawHost{
		time:       t0,
		cpu:        cpuTimes{total: 1000, idle: 800},
		net:        map[string]netCounters{"eth0": {rxBytes: 1000, txBytes: 2000, rxDrop: 0, txDrop: 0}},
		tcpRetrans: 100,
	}
	now := rawHost{
		time:       t0.Add(2 * time.Second),
		numCPU:     4,
		cpu:        cpuTimes{total: 2000, idle: 1600}, // dTotal=1000 dIdle=800 -> busy 0.2
		net:        map[string]netCounters{"eth0": {rxBytes: 3000, txBytes: 6000, rxDrop: 4, txDrop: 0}},
		tcpRetrans: 110,                   // +10 over 2s -> 5/s
		memTotalKB: 1000, memAvailKB: 250, // used 0.75
		psi: &Pressure{CPUSome: 0.3},
	}

	s := computeSample(prev, now, true)
	if s.Warmup {
		t.Fatal("should not be warmup with prevSet")
	}
	if got := s.CPU.BusyFraction; got < 0.19 || got > 0.21 {
		t.Errorf("busy = %v, want ~0.2", got)
	}
	if s.TCP.RetransSegsPerSec != 5 {
		t.Errorf("retrans/s = %v, want 5", s.TCP.RetransSegsPerSec)
	}
	if len(s.Net) != 1 || s.Net[0].RxBytesPerSec != 1000 || s.Net[0].TxBytesPerSec != 2000 {
		t.Errorf("net = %+v, want rx=1000 tx=2000 bps", s.Net)
	}
	if s.Net[0].RxDropsPerSec != 2 {
		t.Errorf("rx drops/s = %v, want 2", s.Net[0].RxDropsPerSec)
	}
	if got := s.Mem.UsedFraction; got < 0.74 || got > 0.76 {
		t.Errorf("mem used = %v, want 0.75", got)
	}
	if !s.CPUSaturated() {
		t.Error("CPUSaturated should be true with PSI cpu_some=0.3")
	}
}

func TestComputeSampleWarmup(t *testing.T) {
	s := computeSample(rawHost{}, rawHost{time: time.Now(), numCPU: 2}, false)
	if !s.Warmup {
		t.Error("first sample (no prev) must be warmup")
	}
}
