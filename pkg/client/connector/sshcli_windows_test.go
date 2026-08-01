package connector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestControlSocketPath(t *testing.T) {
	t.Parallel()

	// Windows OpenSSH does not support ControlMaster multiplexing, so the control socket path is always
	// empty and connections are multiplexed over a SOCKS tunnel instead.
	assert.Empty(t, controlSocketPath())
}
