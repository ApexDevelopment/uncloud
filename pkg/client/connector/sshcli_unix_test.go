//go:build !windows

package connector

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestControlSocketPath(t *testing.T) {
	// Note: Cannot use t.Parallel() because a subtest uses t.Setenv().

	path1 := controlSocketPath()
	path2 := controlSocketPath()

	assert.Equal(t, path1, path2)
	assert.True(t, strings.HasSuffix(path1, ".sock"))
	assert.Contains(t, path1, "%C")

	t.Run("uses XDG_RUNTIME_DIR when set and exists", func(t *testing.T) {
		runDir := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", runDir)

		path := controlSocketPath()
		assert.True(t, strings.HasPrefix(path, runDir))
	})

	t.Run("falls back when XDG_RUNTIME_DIR is set but missing", func(t *testing.T) {
		// WSL2 without systemd sets XDG_RUNTIME_DIR to a path that doesn't exist.
		t.Setenv("XDG_RUNTIME_DIR", "/nonexistent/uncloud-test-xdg")

		path := controlSocketPath()
		assert.NotEmpty(t, path)
		assert.False(t, strings.HasPrefix(path, "/nonexistent/"))
	})
}
