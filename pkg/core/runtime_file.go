package core

// RuntimeFile is materialized by core before docker compose execution, then
// exposed as an environment variable pointing to the generated file path.
type RuntimeFile struct {
	EnvKey   string `json:"env_key"`
	Filename string `json:"filename,omitempty"`
	Content  []byte `json:"-"`
	Mode     uint32 `json:"mode,omitempty"`
}
