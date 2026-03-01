package core

import "encoding/json"

// Secret represents sensitive values that should be redacted in UI/API output.
type Secret struct {
	Value string
}

// NewSecret wraps a raw value as a Secret.
func NewSecret(value string) Secret {
	return Secret{Value: value}
}

// Redacted returns a redacted representation for display.
func (s Secret) Redacted() string {
	if s.Value == "" {
		return ""
	}
	return "REDACTED"
}

// MarshalJSON ensures secrets are never serialized in cleartext.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Redacted())
}

// String returns the redacted value for fmt printing.
func (s Secret) String() string {
	return s.Redacted()
}
