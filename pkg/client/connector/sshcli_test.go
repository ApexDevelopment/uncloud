package connector

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSHCLIConnector_buildSSHArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		config           SSHConnectorConfig
		controlSockPath  string
		useControlMaster bool
		expected         []string
	}{
		{
			name: "basic connection with control socket",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "root@example.com"},
		},
		{
			name: "basic connection without control socket",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
			},
			controlSockPath:  "",
			useControlMaster: true,
			expected:         []string{"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "root@example.com"},
		},
		{
			name: "with custom port",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
				Port: 2222,
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "-p", "2222", "root@example.com"},
		},
		{
			name: "with identity file",
			config: SSHConnectorConfig{
				User:    "root",
				Host:    "example.com",
				KeyPath: "/path/to/key",
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "-i", "/path/to/key", "root@example.com"},
		},
		{
			name: "all options combined",
			config: SSHConnectorConfig{
				User:     "root",
				Host:     "example.com",
				Port:     2222,
				KeyPath:  "/path/to/key",
				SockPath: "/custom/path/uncloud.sock",
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "-p", "2222", "-i", "/path/to/key", "root@example.com"},
		},
		{
			name: "port 0 not included",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
				Port: 0,
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "root@example.com"},
		},
		{
			name: "port 22 included when explicit",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
				Port: 22,
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: true,
			expected:         []string{"-o", "ControlMaster=auto", "-o", "ControlPath=/tmp/test.sock", "-o", "ControlPersist=10m", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "-p", "22", "root@example.com"},
		},
		{
			name: "useControlMaster=false strips control socket options",
			config: SSHConnectorConfig{
				User: "root",
				Host: "example.com",
			},
			controlSockPath:  "/tmp/test.sock",
			useControlMaster: false,
			expected:         []string{"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-T", "root@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &SSHCLIConnector{config: tt.config, controlSockPath: tt.controlSockPath}
			got := c.buildSSHArgs(tt.useControlMaster)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestSSHCLIConnector_socksAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		port     int
		expected string
		wantErr  bool
	}{
		{
			name:     "default port when unset",
			port:     0,
			expected: net.JoinHostPort(defaultSSHSOCKSHost, strconv.Itoa(defaultSSHSOCKSPort)),
		},
		{
			name:     "configured port",
			port:     51180,
			expected: net.JoinHostPort(defaultSSHSOCKSHost, "51180"),
		},
		{
			name:    "port above the valid range",
			port:    70000,
			wantErr: true,
		},
		{
			name:    "negative port",
			port:    -1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &SSHCLIConnector{config: SSHConnectorConfig{SOCKSPort: tt.port}}
			addr, err := c.socksAddr()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, addr)
		})
	}
}
