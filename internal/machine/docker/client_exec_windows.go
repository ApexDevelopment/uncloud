package docker

import (
	"context"

	"github.com/moby/term"
	"github.com/psviderski/uncloud/internal/machine/api/pb"
)

// handleTerminalResize sends the initial window size for TTY sessions. Windows does not deliver terminal
// resize notifications as signals, so the size is not tracked after the session starts.
func handleTerminalResize(_ context.Context, inFd uintptr, stream pb.Docker_ExecContainerClient) error {
	if size, err := term.GetWinsize(inFd); err == nil {
		_ = sendResizeRequest(stream, size)
	}

	return nil
}
