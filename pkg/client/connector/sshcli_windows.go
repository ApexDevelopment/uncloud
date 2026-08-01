package connector

// controlSocketPath returns an empty string on Windows to disable connection multiplexing. Windows OpenSSH
// does not support ControlMaster/ControlPath: the control socket is a Unix domain socket and ssh fails with
// "getsockname failed: Not a socket". Connections are multiplexed over a SOCKS tunnel instead, see
// socksTunnelDialer.
func controlSocketPath() string {
	return ""
}
