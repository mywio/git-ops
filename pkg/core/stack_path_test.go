package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStackPathContainsValidStack(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveStackPath(root, "acme-inc", ".github")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "acme-inc", ".github"), got)
}

func TestResolveStackPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	require.NoError(t, os.Symlink(external, filepath.Join(root, "acme")))

	_, err := ResolveStackPath(root, "acme", "api")
	assert.ErrorContains(t, err, "escapes target directory")
}

func TestValidateStackIdentityRejectsUnsafeComponents(t *testing.T) {
	tests := []struct {
		owner string
		repo  string
	}{
		{owner: "..", repo: "api"},
		{owner: "acme", repo: "../api"},
		{owner: "acme/other", repo: "api"},
		{owner: "acme", repo: `..\\api`},
		{owner: " acme", repo: "api"},
		{owner: "acme", repo: ""},
	}

	for _, tt := range tests {
		t.Run(tt.owner+"_"+tt.repo, func(t *testing.T) {
			assert.Error(t, ValidateStackIdentity(tt.owner, tt.repo))
		})
	}
}

func TestParseStackRefRequiresTwoSafeComponents(t *testing.T) {
	owner, repo, err := ParseStackRef("acme/api")
	require.NoError(t, err)
	assert.Equal(t, "acme", owner)
	assert.Equal(t, "api", repo)

	for _, ref := range []string{"api", "acme/api/extra", "../api", "acme/.."} {
		assert.Error(t, func() error {
			_, _, err := ParseStackRef(ref)
			return err
		}(), ref)
	}
}
