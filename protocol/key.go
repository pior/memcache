package protocol

func IsValidKey(key string) bool {
	if len(key) == 0 || len(key) > MaxKeyLength {
		return false
	}

	for _, b := range []byte(key) {
		if b <= 32 || b == 127 {
			return false
		}
	}

	return true
}
