package ports

import (
	"context"
	"io"
)

// ChannelTarget specifies connection details for reaching the server.
type ChannelTarget struct {
	Host       string
	Port       int
	User       string
	PrivateKey []byte
	// HostPublicKey is the SSH host key the server must present, in authorized_keys format.
	// Required: vps generates the server's bootstrap and durable host keys (AD-10, AD-22),
	// so it always knows which key to expect and refuses any other.
	HostPublicKey []byte
	CustomConn    any // optional in-memory net.Conn for isolated unit testing
}

// PrjPath is the only program vps runs on a server, by absolute path, and the only file
// it pushes by SFTP (AD-5).
const PrjPath = "/usr/local/bin/prj"

// ServerChannel defines the administrative communication port (WireGuard + SSH/SFTP) (AD-4, AD-5).
// It is deliberately narrow: vps runs nothing on a server but prj, and SFTP carries nothing but prj.
// Keys, devices and profiles travel as one JSON document on the stdin of prj system apply or record.
// Not covered yet: the interactive session under dev (story 7) and output streamed while a command runs.
type ServerChannel interface {
	// Connect establishes the SSH/SFTP connection to the target host.
	Connect(ctx context.Context, target ChannelTarget) error

	// PushPrj installs a prj binary at PrjPath: SFTP to a temporary file, SHA256 checked while
	// sending and re-read on the server, mode 0755, then an atomic rename (AD-5, R9).
	PushPrj(ctx context.Context, src io.Reader, expectedSHA256 string) error

	// RunPrj runs PrjPath with args (AD-5). A nil stdin means < /dev/null (AD-22).
	// A non-zero exit comes back as exitCode with a nil err; err reports a transport failure.
	RunPrj(ctx context.Context, args []string, stdin io.Reader) (stdout, stderr []byte, exitCode int, err error)

	// Close terminates active connections.
	Close() error
}
