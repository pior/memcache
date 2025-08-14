package protocol

// Response represents a memcache meta protocol response
type Response struct {
	Status string // Response status: "HD", "VA", "EN", etc.
	Key    string // The key this response is for
	Value  []byte // Value returned (for get operations)
	Flags  Flags  // Meta protocol flags from response
	Error  error  // Any error that occurred
}

// SetFlag sets a flag for the response
func (r *Response) SetFlag(flagType, value string) {
	r.Flags.Set(flagType, value)
}

// GetFlag gets a flag value for the response
func (r *Response) GetFlag(flagType string) (string, bool) {
	return r.Flags.Get(flagType)
}
