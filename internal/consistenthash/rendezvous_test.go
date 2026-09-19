package consistenthash

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// topTwo returns the indexes of the highest- and second-highest-scoring nodes.
func topTwo(keyHash uint64, nodes []string) (first, second int) {
	first, second = -1, -1
	var firstScore, secondScore uint64
	for i, node := range nodes {
		score := RendezvousScore(keyHash, node)
		switch {
		case first < 0 || score > firstScore:
			second, secondScore = first, firstScore
			first, firstScore = i, score
		case second < 0 || score > secondScore:
			second, secondScore = i, score
		}
	}
	return first, second
}

func TestRendezvousScore(t *testing.T) {
	nodes := make([]string, 10)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("10.0.0.%d:11211", i)
	}

	// Every client must place a key on the same node, including clients running
	// different releases during a rollout: any change to the score remaps keys.
	t.Run("scores are stable across releases", func(t *testing.T) {
		tests := []struct {
			keyHash uint64
			node    string
			want    uint64
		}{
			{0, "10.0.0.1:11211", 0xc5dd7169ef9fe658},
			{0, "10.0.0.2:11211", 0x4124972d1d85b440},
			{1, "10.0.0.1:11211", 0xc52f214118517b19},
			{0xdeadbeef, "10.0.0.2:11211", 0x56e43b404ed1ae2d},
			{0xffffffffffffffff, "10.0.0.1:11211", 0x15a0008c07a15eaa},
		}
		for _, tt := range tests {
			if got := RendezvousScore(tt.keyHash, tt.node); got != tt.want {
				t.Errorf("RendezvousScore(%#x, %q) = %#x, want %#x", tt.keyHash, tt.node, got, tt.want)
			}
		}
	})

	t.Run("depends on the key and the node", func(t *testing.T) {
		if RendezvousScore(1, nodes[0]) == RendezvousScore(2, nodes[0]) {
			t.Error("score ignores the key")
		}
		if RendezvousScore(1, nodes[0]) == RendezvousScore(1, nodes[1]) {
			t.Error("score ignores the node")
		}
	})

	// A fixed seed keeps the sample, and so the test, deterministic.
	const keys = 100_000
	rng := rand.New(rand.NewPCG(1, 2))
	wins := make([]int, len(nodes))
	runnerUps := make([]int, len(nodes)) // runner-up of the keys nodes[0] wins
	for range keys {
		first, second := topTwo(rng.Uint64(), nodes)
		wins[first]++
		if first == 0 {
			runnerUps[second]++
		}
	}

	// A plain XOR of the key and node hashes would rank nodes by their shared
	// high bits, so some nodes would win far more often than others.
	t.Run("each node wins an equal share of keys", func(t *testing.T) {
		want := keys / len(nodes)
		for i, n := range wins {
			if n < want*9/10 || n > want*11/10 {
				t.Errorf("%s wins %d keys, want %d +/- 10%%", nodes[i], n, want)
			}
		}
	})

	// When a node leaves, each of its keys moves to its runner-up. Those keys
	// must spread evenly over the remaining nodes rather than pile onto one.
	t.Run("runner-ups are spread evenly", func(t *testing.T) {
		want := wins[0] / (len(nodes) - 1)
		for i, n := range runnerUps[1:] {
			if n < want*8/10 || n > want*12/10 {
				t.Errorf("%s is runner-up for %d keys, want %d +/- 20%%", nodes[i+1], n, want)
			}
		}
	})
}
