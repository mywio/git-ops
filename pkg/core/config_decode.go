package core

import "gopkg.in/yaml.v3"

// DecodeConfigSection decodes a config section into a struct.
// It is safe to call with a nil or empty section.
func DecodeConfigSection(section map[string]any, out any) error {
	if len(section) == 0 {
		return nil
	}
	data, err := yaml.Marshal(section)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
