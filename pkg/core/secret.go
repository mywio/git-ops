package core

import "encoding/json"

// Secret wraps a sensitive string value that should be redacted in logs, API
// responses, and UI serialization.
type Secret struct {
	Value string
}

// NewSecret wraps a raw string as a Secret.
func NewSecret(value string) Secret {
	return Secret{Value: value}
}

// Redacted returns the display form used when exposing the secret value.
func (s Secret) Redacted() string {
	if s.Value == "" {
		return ""
	}
	return "REDACTED"
}

// MarshalJSON serializes the redacted form instead of the underlying value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Redacted())
}

// String returns the redacted display form for fmt.Stringer-compatible output.
func (s Secret) String() string {
	return s.Redacted()
}
