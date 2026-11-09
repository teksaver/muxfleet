package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"muxfleet/internal/ports"
)

var (
	ErrNotConnected     = errors.New("canal SSH non connecté")
	ErrChecksumMismatch = errors.New("empreinte SHA256 non conforme")
	ErrNoHostKey        = errors.New("clé d'hôte SSH attendue manquante")
	ErrHostKeyMismatch  = errors.New("clé d'hôte SSH inattendue")
)

// SSHClient implements ports.ServerChannel using SSH and SFTP (AD-4, AD-5).
type SSHClient struct {
	mu         sync.Mutex
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	target     ports.ChannelTarget
	prjPath    string // always ports.PrjPath, except in this package's tests
}

// NewSSHClient creates an unconfigured SSHClient.
func NewSSHClient() *SSHClient {
	return &SSHClient{prjPath: ports.PrjPath}
}

// PushPrj installs prj at its only path, /usr/local/bin/prj (AD-5, R9).
func (c *SSHClient) PushPrj(ctx context.Context, src io.Reader, expectedSHA256 string) error {
	return c.pushAtomic(ctx, src, c.prjPath, 0755, expectedSHA256)
}

// RunPrj runs prj by absolute path, the only command vps runs on a server (AD-5).
// Each argument is quoted for the remote shell; a nil stdin becomes < /dev/null (AD-22).
func (c *SSHClient) RunPrj(ctx context.Context, args []string, stdin io.Reader) (stdout, stderr []byte, exitCode int, err error) {
	words := make([]string, 0, len(args)+1)
	words = append(words, shellQuote(c.prjPath))
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	cmd := strings.Join(words, " ")
	if stdin == nil {
		cmd += " < /dev/null"
	}
	return c.execute(ctx, cmd, stdin)
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Connect establishes the SSH and SFTP connections to the target.
func (c *SSHClient) Connect(ctx context.Context, target ports.ChannelTarget) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if target.Port == 0 {
		target.Port = 22
	}
	if target.User == "" {
		target.User = "root"
	}

	// AD-10: vps generated the server's host key, so it pins it and refuses any other.
	if len(target.HostPublicKey) == 0 {
		return ErrNoHostKey
	}
	wantHostKey, _, _, _, err := ssh.ParseAuthorizedKey(target.HostPublicKey)
	if err != nil {
		return fmt.Errorf("analyse de la clé d'hôte attendue échouée: %w", err)
	}

	var authMethods []ssh.AuthMethod
	if len(target.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(target.PrivateKey)
		if err != nil {
			return fmt.Errorf("analyse de la clé privée SSH échouée: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	config := &ssh.ClientConfig{
		User:              target.User,
		Auth:              authMethods,
		HostKeyCallback:   pinnedHostKey(wantHostKey),
		HostKeyAlgorithms: hostKeyAlgorithms(wantHostKey),
		Timeout:           10 * time.Second,
	}

	addr := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))

	var conn net.Conn
	if custom, ok := target.CustomConn.(net.Conn); ok && custom != nil {
		conn = custom
	} else {
		var d net.Dialer
		var err error
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("connexion TCP vers %s échouée: %w", addr, err)
		}
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("handshake SSH avec %s échoué: %w", addr, err)
	}

	c.sshClient = ssh.NewClient(sshConn, chans, reqs)

	sftpClient, err := sftp.NewClient(c.sshClient)
	if err != nil {
		c.sshClient.Close()
		return fmt.Errorf("initialisation du sous-système SFTP échouée: %w", err)
	}

	c.sftpClient = sftpClient
	c.target = target
	return nil
}

// pinnedHostKey accepts only the expected host key and names both fingerprints otherwise.
func pinnedHostKey(want ssh.PublicKey) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), want.Marshal()) {
			return fmt.Errorf("%w : reçue %s, attendue %s", ErrHostKeyMismatch, ssh.FingerprintSHA256(key), ssh.FingerprintSHA256(want))
		}
		return nil
	}
}

// hostKeyAlgorithms asks the server for the pinned key's type, so it does not offer another of its keys.
func hostKeyAlgorithms(k ssh.PublicKey) []string {
	if k.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{k.Type()}
}

// pushAtomic pushes src via SFTP to remotePath.tmp, verifies expectedSHA256,
// sets file mode (0755), and atomically renames it to remotePath (AD-5, R9).
func (c *SSHClient) pushAtomic(ctx context.Context, src io.Reader, remotePath string, mode os.FileMode, expectedSHA256 string) error {
	c.mu.Lock()
	sftpCl := c.sftpClient
	c.mu.Unlock()

	if sftpCl == nil {
		return ErrNotConnected
	}

	// 1. Ensure parent directory exists
	dir := filepath.Dir(remotePath)
	if err := sftpCl.MkdirAll(dir); err != nil {
		return fmt.Errorf("création du dossier distant %s échouée: %w", dir, err)
	}

	tempPath := remotePath + ".tmp"

	// 2. Open temporary remote file for writing
	remoteFile, err := sftpCl.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("création du fichier temporaire distant %s échouée: %w", tempPath, err)
	}

	// 3. Compute local SHA256 while copying to remote
	hasher := sha256.New()
	writer := io.MultiWriter(remoteFile, hasher)

	if _, err := io.Copy(writer, src); err != nil {
		remoteFile.Close()
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("transfert SFTP vers %s échoué: %w", tempPath, err)
	}

	if err := remoteFile.Close(); err != nil {
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("fermeture du fichier temporaire %s échouée: %w", tempPath, err)
	}

	computedSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && computedSHA256 != expectedSHA256 {
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("%w: calculé=%s, attendu=%s", ErrChecksumMismatch, computedSHA256, expectedSHA256)
	}

	// 4. Verify remote file SHA256 directly from the uploaded file
	remoteSHA, err := c.remoteSHA256(ctx, tempPath)
	if err != nil {
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("vérification de l'empreinte distante échouée: %w", err)
	}
	if remoteSHA != computedSHA256 {
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("%w: fichier distant altéré (local=%s, distant=%s)", ErrChecksumMismatch, computedSHA256, remoteSHA)
	}

	// 5. Chmod 0755
	if mode == 0 {
		mode = 0755
	}
	if err := sftpCl.Chmod(tempPath, mode); err != nil {
		_ = sftpCl.Remove(tempPath)
		return fmt.Errorf("chmod %o sur %s échoué: %w", mode, tempPath, err)
	}

	// 6. POSIX atomic rename
	if err := sftpCl.PosixRename(tempPath, remotePath); err != nil {
		// Fallback to standard Rename
		if err2 := sftpCl.Rename(tempPath, remotePath); err2 != nil {
			_ = sftpCl.Remove(tempPath)
			return fmt.Errorf("renommage atomique %s -> %s échoué: %w", tempPath, remotePath, err2)
		}
	}

	return nil
}

// remoteSHA256 computes the SHA256 sum of a remote file via SFTP.
// If the remote file does not exist, returns ("", nil).
func (c *SSHClient) remoteSHA256(ctx context.Context, remotePath string) (string, error) {
	c.mu.Lock()
	sftpCl := c.sftpClient
	c.mu.Unlock()

	if sftpCl == nil {
		return "", ErrNotConnected
	}

	file, err := sftpCl.Open(remotePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("lecture du fichier distant pour calcul SHA256 échouée: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// execute runs a command on the remote server with the given stdin. Only RunPrj calls it (AD-5).
func (c *SSHClient) execute(ctx context.Context, cmd string, stdin io.Reader) (stdout, stderr []byte, exitCode int, err error) {
	c.mu.Lock()
	sshCl := c.sshClient
	c.mu.Unlock()

	if sshCl == nil {
		return nil, nil, 1, ErrNotConnected
	}

	session, err := sshCl.NewSession()
	if err != nil {
		return nil, nil, 1, fmt.Errorf("création de session SSH échouée: %w", err)
	}
	defer session.Close()

	// Bind context cancellation to session closure
	stopped := make(chan struct{})
	defer close(stopped)

	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-stopped:
		}
	}()

	if stdin != nil {
		session.Stdin = stdin
	}

	var stdoutBuf, stderrBuf []byte
	stdoutReader, err := session.StdoutPipe()
	if err != nil {
		return nil, nil, 1, err
	}
	stderrReader, err := session.StderrPipe()
	if err != nil {
		return nil, nil, 1, err
	}

	if err := session.Start(cmd); err != nil {
		return nil, nil, 1, fmt.Errorf("démarrage de la commande distante échoué: %w", err)
	}

	stdoutBuf, _ = io.ReadAll(stdoutReader)
	stderrBuf, _ = io.ReadAll(stderrReader)

	waitErr := session.Wait()
	if ctx.Err() != nil {
		return stdoutBuf, stderrBuf, 1, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(waitErr, &exitErr) {
			return stdoutBuf, stderrBuf, exitErr.ExitStatus(), nil
		}
		return stdoutBuf, stderrBuf, 1, waitErr
	}

	return stdoutBuf, stderrBuf, 0, nil
}

// Close terminates the active SFTP and SSH connections.
func (c *SSHClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var firstErr error
	if c.sftpClient != nil {
		if err := c.sftpClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.sftpClient = nil
	}
	if c.sshClient != nil {
		if err := c.sshClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.sshClient = nil
	}
	return firstErr
}
