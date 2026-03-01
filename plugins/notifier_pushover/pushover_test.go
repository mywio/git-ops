package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePatternsPushover(t *testing.T) {
	input := []string{" notify_* ", "", "deploy_*", "notify_*", "  "}
	out := normalizePatterns(input)
	assert.Equal(t, []string{"notify_*", "deploy_*"}, out)
}

func TestParseSubscribePatternsPushover(t *testing.T) {
	tests := []struct {
		name    string
		section map[string]any
		want    []string
	}{
		{
			name:    "missing",
			section: map[string]any{},
			want:    nil,
		},
		{
			name: "string_list",
			section: map[string]any{
				"subscribe": []string{" notify_* ", "deploy_*"},
			},
			want: []string{"notify_*", "deploy_*"},
		},
		{
			name: "any_list",
			section: map[string]any{
				"subscribe": []any{" notify_*", 123, "deploy_*", "notify_*"},
			},
			want: []string{"notify_*", "123", "deploy_*"},
		},
		{
			name: "csv_string",
			section: map[string]any{
				"subscribe": "notify_*, deploy_*",
			},
			want: []string{"notify_*", "deploy_*"},
		},
		{
			name: "scalar",
			section: map[string]any{
				"subscribe": 42,
			},
			want: []string{"42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := parseSubscribePatterns(tt.section)
			assert.Equal(t, tt.want, out)
		})
	}
}
