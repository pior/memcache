package internal

// JumpHash implements the Jump consistent hashing algorithm.
// Copied from: https://github.com/dgryski/go-jump
// Google's "Jump" Consistent Hash function: https://arxiv.org/abs/1406.2294
func JumpHash(key uint64, numBuckets int) int {
	if numBuckets <= 0 {
		return 0
	}

	var b int64 = -1
	var j int64

	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}

	return int(b)
}
