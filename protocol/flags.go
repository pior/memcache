package protocol

// Flag represents a meta protocol flag
type Flag struct {
	Type  string // Flag type: "v", "D", "M", etc.
	Value string // Flag value (empty for flags without values)
}

// Flags represents a collection of meta protocol flags
type Flags []Flag

// Set sets a flag value, updating existing flag or adding new one
func (f *Flags) Set(flagType, value string) {
	// Check if flag already exists and update it
	for i := range *f {
		if (*f)[i].Type == flagType {
			(*f)[i].Value = value
			return
		}
	}
	// Flag doesn't exist, append new one
	*f = append(*f, Flag{Type: flagType, Value: value})
}

// Get gets a flag value, returning the value and whether it exists
func (f Flags) Get(flagType string) (string, bool) {
	for _, flag := range f {
		if flag.Type == flagType {
			return flag.Value, true
		}
	}
	return "", false
}
