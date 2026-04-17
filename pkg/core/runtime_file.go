package core

// RuntimeFile describes a file that core materializes before docker compose
// execution and exposes through an environment variable pointing at the file
// path.
type RuntimeFile struct {
	EnvKey   string `json:"env_key"`
	Filename string `json:"filename,omitempty"`
	Content  []byte `json:"-"`
	Mode     uint32 `json:"mode,omitempty"`
}
