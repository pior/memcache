package hoststat

import (
	"bufio"
	"strconv"
	"strings"
)

// Pure parsers for /proc files. They take file contents so they can be unit
// tested against captured fixtures without a Linux host.

// cpuTimes holds the aggregate CPU jiffies needed to compute utilization.
type cpuTimes struct {
	total uint64
	idle  uint64 // idle + iowait
}

// parseProcStat parses the aggregate "cpu" line of /proc/stat.
func parseProcStat(data string) (cpuTimes, bool) {
	for line := range strings.Lines(data) {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:] // user nice system idle iowait irq softirq steal ...
		var ct cpuTimes
		for i, f := range fields {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				continue
			}
			ct.total += v
			if i == 3 || i == 4 { // idle, iowait
				ct.idle += v
			}
		}
		return ct, true
	}
	return cpuTimes{}, false
}

// parseLoadavg parses /proc/loadavg -> the three load averages.
func parseLoadavg(data string) ([3]float64, bool) {
	fields := strings.Fields(data)
	if len(fields) < 3 {
		return [3]float64{}, false
	}
	var la [3]float64
	for i := range 3 {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return [3]float64{}, false
		}
		la[i] = v
	}
	return la, true
}

// parseMeminfo parses /proc/meminfo -> total and available KB.
func parseMeminfo(data string) (totalKB, availKB uint64, ok bool) {
	var haveTotal, haveAvail bool
	for line := range strings.Lines(data) {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			totalKB, haveTotal = v, true
		case "MemAvailable":
			availKB, haveAvail = v, true
		}
	}
	return totalKB, availKB, haveTotal && haveAvail
}

// netCounters holds the cumulative per-interface counters we track.
type netCounters struct {
	rxBytes, rxDrop, txBytes, txDrop uint64
}

// parseNetDev parses /proc/net/dev -> per-interface cumulative counters,
// excluding the loopback interface.
func parseNetDev(data string) map[string]netCounters {
	out := make(map[string]netCounters)
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		iface, rest, found := strings.Cut(line, ":")
		if !found {
			continue // header lines have no colon in the iface position
		}
		iface = strings.TrimSpace(iface)
		if iface == "" || iface == "lo" {
			continue
		}
		f := strings.Fields(rest)
		// Layout: rx: bytes packets errs drop fifo frame compressed multicast
		//         tx: bytes packets errs drop ...
		if len(f) < 16 {
			continue
		}
		nc := netCounters{}
		nc.rxBytes = mustU64(f[0])
		nc.rxDrop = mustU64(f[3])
		nc.txBytes = mustU64(f[8])
		nc.txDrop = mustU64(f[11])
		out[iface] = nc
	}
	return out
}

// parseTCPRetrans parses /proc/net/snmp -> cumulative TCP RetransSegs.
func parseTCPRetrans(data string) (uint64, bool) {
	var header, values string
	for line := range strings.Lines(data) {
		if !strings.HasPrefix(line, "Tcp:") {
			continue
		}
		if header == "" {
			header = line // the first Tcp: line is the column header
		} else {
			values = line // the second is the values
			break
		}
	}
	if header == "" || values == "" {
		return 0, false
	}
	cols := strings.Fields(header)
	vals := strings.Fields(values)
	for i, c := range cols {
		if c == "RetransSegs" && i < len(vals) {
			v, err := strconv.ParseUint(vals[i], 10, 64)
			return v, err == nil
		}
	}
	return 0, false
}

// parsePSISome parses a /proc/pressure/* file -> the "some avg10" fraction
// (0..1; the file reports a percentage).
func parsePSISome(data string) (float64, bool) {
	for line := range strings.Lines(data) {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for f := range strings.FieldsSeq(line) {
			if k, v, ok := strings.Cut(f, "="); ok && k == "avg10" {
				pct, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return 0, false
				}
				return pct / 100, true
			}
		}
	}
	return 0, false
}

func mustU64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}
