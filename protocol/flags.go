package protocol

import (
	"strings"
)

// Flag represents a meta protocol flag
type Flag struct {
	Type  FlagType // Flag type: "v", "D", "M", etc.
	Value string   // Flag value (empty for flags without values)
}

// Flags represents a collection of meta protocol flags
type Flags []Flag

// Set sets a flag value, updating existing flag or adding new one
func (f *Flags) Set(flagType FlagType, value string) {
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
func (f Flags) Get(flagType FlagType) (string, bool) {
	for _, flag := range f {
		if flag.Type == flagType {
			return flag.Value, true
		}
	}
	return "", false
}

func (f *Flags) parse(parts []string) {
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if len(part) > 1 {
			*f = append(*f, Flag{Type: FlagType(part[0]), Value: part[1:]})
		} else {
			*f = append(*f, Flag{Type: FlagType(part[0]), Value: ""})
		}
	}
}

func (f Flags) String() string {
	var sb strings.Builder
	for i, flag := range f {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(string(flag.Type))
		sb.WriteString(flag.Value)
	}
	return sb.String()
}
