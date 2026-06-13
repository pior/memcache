package cloud

import (
	"fmt"
	"strconv"
	"strings"
)

// ServerPlacement is a count of server VMs to create in a zone.
type ServerPlacement struct {
	Zone  string
	Count int
}

// RegionOf derives the region from a zone, e.g. "us-central1-a" -> "us-central1".
func RegionOf(zone string) string {
	if i := strings.LastIndexByte(zone, '-'); i > 0 {
		return zone[:i]
	}
	return zone
}

// Tier classifies a server's network distance from the client zone.
type Tier string

const (
	TierSameZone    Tier = "same-zone"
	TierCrossZone   Tier = "cross-zone"
	TierCrossRegion Tier = "cross-region"
)

// TierOf classifies serverZone relative to clientZone.
func TierOf(serverZone, clientZone string) Tier {
	switch {
	case serverZone == clientZone:
		return TierSameZone
	case RegionOf(serverZone) == RegionOf(clientZone):
		return TierCrossZone
	default:
		return TierCrossRegion
	}
}

// farRegion returns a deliberately distant region from the client's, for the
// "global" placement's cross-region tier.
func farRegion(clientRegion string) string {
	if strings.HasPrefix(clientRegion, "us") || strings.HasPrefix(clientRegion, "northamerica") {
		return "asia-southeast1"
	}
	return "us-central1"
}

// ParsePlacement resolves a placement spec into per-zone server VM counts.
// spec is either a preset ("local", "regional", "global") expanded over total
// VMs relative to clientZone, or a custom "zone:count,zone:count" list (whose
// counts are authoritative and total is ignored).
func ParsePlacement(spec, clientZone string, total int) ([]ServerPlacement, error) {
	spec = strings.TrimSpace(spec)
	region := RegionOf(clientZone)

	switch spec {
	case "", "local":
		return []ServerPlacement{{Zone: clientZone, Count: total}}, nil
	case "regional":
		zones := []string{region + "-a", region + "-b", region + "-c"}
		return distribute(zones, total), nil
	case "global":
		far := farRegion(region)
		zones := []string{clientZone, region + "-b", far + "-b"}
		return distribute(zones, total), nil
	}

	// custom: zone:count,zone:count
	var out []ServerPlacement
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		zone, countStr, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("invalid placement entry %q (want zone:count)", part)
		}
		count, err := strconv.Atoi(strings.TrimSpace(countStr))
		if err != nil || count <= 0 {
			return nil, fmt.Errorf("invalid count in placement entry %q", part)
		}
		out = append(out, ServerPlacement{Zone: strings.TrimSpace(zone), Count: count})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty placement spec")
	}
	return out, nil
}

// distribute spreads total VMs round-robin across zones, dropping zones that
// receive none (when total < len(zones)).
func distribute(zones []string, total int) []ServerPlacement {
	counts := make([]int, len(zones))
	for i := range total {
		counts[i%len(zones)]++
	}
	var out []ServerPlacement
	for i, z := range zones {
		if counts[i] > 0 {
			out = append(out, ServerPlacement{Zone: z, Count: counts[i]})
		}
	}
	return out
}

// Regions returns the distinct regions used by a placement (for subnet setup).
func Regions(placements []ServerPlacement, clientZone string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(region string) {
		if !seen[region] {
			seen[region] = true
			out = append(out, region)
		}
	}
	add(RegionOf(clientZone))
	for _, p := range placements {
		add(RegionOf(p.Zone))
	}
	return out
}
