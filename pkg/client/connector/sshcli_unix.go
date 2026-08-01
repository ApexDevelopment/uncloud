//go:build !windows

package connector

import (
	"fmt"
	"os"
	"path/filepath"
)

// controlSocketPath returns a unique control socket path for the SSH connection.
// Returns an empty string if unable to find or create a suitable path.
func controlSocketPath() string {
	// %C is expanded by `ssh` to a hash of user, local and remote hostnames, port, and the contents
	// of the ProxyJump option. This ensures that shared connections are uniquely identified.
	sockName := fmt.Sprintf("uc_control_%%C.sock")

	// Prefer XDG_RUNTIME_DIR if set and the directory exists, fall back to ~/.ssh if it exists.
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		// On WSL2 without systemd, XDG_RUNTIME_DIR may be set to /run/user/$UID that doesn't actually exist,
		// so existence must be verified before use: https://github.com/psviderski/uncloud/issues/319.
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, sockName)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		sshDir := filepath.Join(home, ".ssh")
		if fi, sErr := os.Stat(sshDir); sErr == nil && fi.IsDir() {
			return filepath.Join(sshDir, sockName)
		}
	}

	// Last resort: create a subdirectory in temp with restricted permissions.
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("uncloud-%d", os.Getuid()))
	path := filepath.Join(tmpDir, sockName)
	if len(path)-2+40 < 104 { // 40 chars for %C hash, 104 is typical UNIX socket path limit
		if err := os.MkdirAll(tmpDir, 0o700); err == nil {
			return path
		}
	}

	return ""
}
