package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/cli/cli/connhelper/commandconn"
	"github.com/psviderski/uncloud/internal/grpcversion"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultSSHSOCKSHost = "127.0.0.1"
	// defaultSSHSOCKSPort is the local port used for the ssh -D SOCKS tunnel. Override it with
	// SSHConnectorConfig.SOCKSPort if another process already uses the default.
	defaultSSHSOCKSPort = 51022
	// socksReadyTimeout bounds how long to wait for ssh to authenticate and bind the local SOCKS port.
	socksReadyTimeout = 10 * time.Second
)

// SSHCLIConnector establishes a connection to the machine API by executing SSH CLI
// and running `uncloudd dial-stdio` on the remote machine.
type SSHCLIConnector struct {
	config SSHConnectorConfig
	// Path to SSH control socket for connection reuse.
	controlSockPath string
	// fwdCheckOnce ensures the TCP forwarding check runs only once per connector.
	fwdCheckOnce sync.Once
	// fwdCheckErr caches the result of the TCP forwarding check.
	fwdCheckErr error

	// socksOnce establishes the SOCKS tunnel on first use to multiplex DialContext connections when
	// ControlMaster is unavailable (see socksTunnelDialer).
	socksOnce   sync.Once
	socksDialer proxy.ContextDialer
	socksErr    error
	socksCancel context.CancelFunc
}

func NewSSHCLIConnector(cfg *SSHConnectorConfig) *SSHCLIConnector {
	return &SSHCLIConnector{
		config:          *cfg,
		controlSockPath: controlSocketPath(),
	}
}

func (c *SSHCLIConnector) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	// Validate SSH connectivity by running a no-op command on the remote machine. This also
	// establishes the control socket (ControlMaster=auto) so subsequent connections reuse it.
	probeArgs := append(c.buildSSHArgs(true), "true")
	probe := exec.CommandContext(ctx, "ssh", probeArgs...)
	if output, err := probe.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("SSH connection to '%s': %w: %s",
			c.config.Destination(), err, strings.TrimSpace(string(output)))
	}

	// Create gRPC client with a dialer that spawns new SSH connections on demand,
	// reusing the control socket established above.
	grpcConn, err := grpc.NewClient(
		"passthrough:///", // Dummy target since we're using a custom dialer.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(defaultServiceConfig),
		grpc.WithUnaryInterceptor(grpcversion.ClientUnaryInterceptor),
		grpc.WithStreamInterceptor(grpcversion.ClientStreamInterceptor),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialArgs := append(c.buildSSHArgs(true), "uncloudd", "dial-stdio")
			if c.config.SockPath != "" {
				dialArgs = append(dialArgs, "--socket", c.config.SockPath)
			}

			conn, err := commandconn.New(ctx, "ssh", dialArgs...)
			if err != nil {
				return nil, fmt.Errorf("SSH connection to '%s': %w", c.config.Destination(), err)
			}
			return conn, nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create machine API client: %w", err)
	}

	return grpcConn, nil
}

// buildSSHArgs constructs the SSH command arguments with connection options and destination. The options
// include control socket settings for connection reuse if the path is configured and useControlMaster is true.
// The remote command is not included and should be appended by the caller.
func (c *SSHCLIConnector) buildSSHArgs(useControlMaster bool) []string {
	var args []string

	// Add control socket options for connection reuse if available.
	if useControlMaster && c.controlSockPath != "" {
		args = append(args, "-o", "ControlMaster=auto")
		args = append(args, "-o", "ControlPath="+c.controlSockPath)

		// Keep the established connection alive for a short duration after the last session closes to allow reuse.
		controlPersist := "10m"
		// Override the default duration with the UNCLOUD_SSH_CONTROL_PERSIST env variable.
		if v := os.Getenv("UNCLOUD_SSH_CONTROL_PERSIST"); v != "" {
			controlPersist = v
		}
		args = append(args, "-o", "ControlPersist="+controlPersist)
	}

	// Add connection timeout to fail fast when node is down.
	args = append(args, "-o", "ConnectTimeout=5")
	// Disable interactive prompts (e.g., passphrase input) to prevent interference with the TUI.
	// Authentication must succeed non-interactively via SSH agent or unencrypted key.
	args = append(args, "-o", "BatchMode=yes")
	// Disable host key checking for parity with go+ssh.
	args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	// Disable pseudo-terminal allocation to prevent SSH from executing as a login shell.
	args = append(args, "-T")

	// Add port if specified.
	if c.config.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.config.Port))
	}

	// Add identity file if specified (backward compatibility with SSHKeyFile).
	if c.config.KeyPath != "" {
		args = append(args, "-i", c.config.KeyPath)
	}

	// Add [user@]host destination. Options may still follow it: ssh resumes option parsing after the
	// destination, which is how callers append flags such as -W or -D.
	args = append(args, c.config.Destination())

	return args
}

// Dialer returns a proxy dialer for establishing connections within the cluster through SSH tunnels.
func (c *SSHCLIConnector) Dialer() (proxy.ContextDialer, error) {
	if c.config == (SSHConnectorConfig{}) {
		return nil, fmt.Errorf("SSH connector not configured")
	}

	return c, nil
}

// DialContext establishes a connection to the target address through an SSH tunnel using -W flag.
func (c *SSHCLIConnector) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}

	c.fwdCheckOnce.Do(func() {
		c.fwdCheckErr = c.CheckTCPForwarding(ctx)
		if c.fwdCheckErr != nil {
			// Close the cached ControlMaster so the next call picks up the new sshd policy once the
			// user enables forwarding. Fresh context so close runs even if the parent already timed out.
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer closeCancel()
			c.CloseControlMaster(closeCtx)
		}
	})
	if c.fwdCheckErr != nil {
		return nil, c.fwdCheckErr
	}

	// When ControlMaster multiplexing is unavailable (e.g. on Windows), route the connection through a single
	// long-lived SOCKS tunnel instead of spawning a separate ssh process per dial. Without this, a burst of
	// parallel layer uploads to unregistry opens many simultaneous SSH connections, which the server rejects
	// during key exchange ("kex_exchange_identification: Connection reset"). The SOCKS tunnel multiplexes all
	// dials as port-forwarding channels over one SSH connection, which sshd does not rate-limit.
	if c.controlSockPath == "" {
		dialer, err := c.socksTunnelDialer(ctx)
		if err != nil {
			return nil, err
		}
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, fmt.Errorf("SSH connection to '%s' for dialing '%s': %w", c.config.Destination(), address, err)
		}
		return conn, nil
	}

	args := append(c.buildSSHArgs(true), "-W", address)
	conn, err := commandconn.New(ctx, "ssh", args...)
	if err != nil {
		return nil, fmt.Errorf("SSH connection to '%s' for dialing '%s': %w", c.config.Destination(), address, err)
	}

	return conn, nil
}

// socksTunnelDialer starts a single `ssh -D` SOCKS proxy on first use and returns a dialer that routes
// connections through it. The tunnel is started once per connector and reused by all subsequent dials,
// so any number of concurrent connections are multiplexed over a single SSH connection. The tunnel is torn
// down by Close. It uses the system ssh client, so it honours ~/.ssh/config host aliases, agents and keys
// exactly like the rest of the connector.
func (c *SSHCLIConnector) socksTunnelDialer(ctx context.Context) (proxy.ContextDialer, error) {
	c.socksOnce.Do(func() {
		socksAddr, err := c.socksAddr()
		if err != nil {
			c.socksErr = err
			return
		}

		// The tunnel must outlive the dial context that triggered it, so Close cancels this background context.
		tunnelCtx, cancel := context.WithCancel(context.Background())
		args := append(c.buildSSHArgs(false), "-o", "ExitOnForwardFailure=yes", "-N", "-D", socksAddr)
		cmd := exec.CommandContext(tunnelCtx, "ssh", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err = cmd.Start(); err != nil {
			cancel()
			c.socksErr = fmt.Errorf("start SSH SOCKS tunnel to '%s': %w", c.config.Destination(), err)
			return
		}

		var sshErr error
		sshDone := make(chan struct{})
		go func() {
			sshErr = cmd.Wait()
			close(sshDone)
		}()

		if err = waitForSOCKSReady(ctx, socksAddr, sshDone, &sshErr); err != nil {
			cancel()
			<-sshDone
			if detail := strings.TrimSpace(stderr.String()); detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
			c.socksErr = fmt.Errorf(
				"establish SSH SOCKS tunnel to '%s' on %s: %w. If that local port is unavailable, "+
					"pick another one with --ssh-socks-port",
				c.config.Destination(), socksAddr, err,
			)
			return
		}

		dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
		if err != nil {
			cancel()
			c.socksErr = fmt.Errorf("create SOCKS dialer: %w", err)
			return
		}
		ctxDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			cancel()
			c.socksErr = fmt.Errorf("SOCKS dialer does not support context dialing")
			return
		}

		c.socksCancel = cancel
		c.socksDialer = ctxDialer
	})

	return c.socksDialer, c.socksErr
}

// socksAddr returns the local address the ssh SOCKS tunnel listens on.
func (c *SSHCLIConnector) socksAddr() (string, error) {
	port := c.config.SOCKSPort
	if port == 0 {
		port = defaultSSHSOCKSPort
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid SSH SOCKS port %d: must be a TCP port from 1 to 65535", port)
	}

	return net.JoinHostPort(defaultSSHSOCKSHost, strconv.Itoa(port)), nil
}

// waitForSOCKSReady polls addr until a TCP connection succeeds, ssh exits, the context is cancelled, or
// socksReadyTimeout elapses. It gives the ssh SOCKS proxy time to authenticate and bind its local port.
func waitForSOCKSReady(ctx context.Context, addr string, sshDone <-chan struct{}, sshErr *error) error {
	ctx, cancel := context.WithTimeout(ctx, socksReadyTimeout)
	defer cancel()

	dialer := net.Dialer{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-sshDone:
			if *sshErr == nil {
				return errors.New("ssh exited before the SOCKS proxy became ready")
			}
			return fmt.Errorf("ssh exited before the SOCKS proxy became ready: %w", *sshErr)
		case <-ctx.Done():
			return fmt.Errorf("SOCKS proxy did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// CheckTCPForwarding returns an actionable error when the remote SSH server doesn't allow TCP forwarding.
func (c *SSHCLIConnector) CheckTCPForwarding(ctx context.Context) error {
	// Do not use ControlMaster because disabled forwarding and a refused port both surface as
	// "Session open refused by peer" over it and can't be told apart.
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Request forwarding to a port that is almost never in use (:1) so sshd rejects the channel if forwarding
	// is disabled or fails to connect otherwise.
	args := append(c.buildSSHArgs(false), "-W", "127.0.0.1:1")
	output, _ := exec.CommandContext(probeCtx, "ssh", args...).CombinedOutput()
	if strings.Contains(string(output), "administratively prohibited") {
		return fmt.Errorf("SSH TCP forwarding appears to be disabled on '%s': ensure 'AllowTcpForwarding yes' "+
			"is set in /etc/ssh/sshd_config on the remote machine and restart sshd (sudo systemctl restart ssh), "+
			"then retry",
			c.config.Destination())
	}

	return nil
}

// CloseControlMaster terminates the SSH ControlMaster process for this destination so the next connection starts
// a fresh SSH session. No-op if no master is running or the control socket is not configured. Errors are ignored.
func (c *SSHCLIConnector) CloseControlMaster(ctx context.Context) {
	if c.controlSockPath == "" {
		return
	}
	args := append(c.buildSSHArgs(true), "-O", "exit")
	_ = exec.CommandContext(ctx, "ssh", args...).Run()
}

func (c *SSHCLIConnector) Close() error {
	// Tear down the SOCKS tunnel if one was started (Windows/no-ControlMaster path).
	if c.socksCancel != nil {
		c.socksCancel()
	}
	// Individual connections are managed by gRPC and closed when the gRPC connection closes.
	// The SSH control socket may persist for connection reuse across CLI invocations.
	return nil
}
