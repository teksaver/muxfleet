package channel

import "context"

// Test hooks: this package's tests serve SFTP from a temporary directory, not from /usr/local/bin.

func (c *SSHClient) SetPrjPathForTest(p string) { c.prjPath = p }

func (c *SSHClient) PrjPathForTest() string { return c.prjPath }

func (c *SSHClient) RemoteSHA256ForTest(ctx context.Context, path string) (string, error) {
	return c.remoteSHA256(ctx, path)
}
