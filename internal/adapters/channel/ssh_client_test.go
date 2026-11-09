package channel_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"muxfleet/internal/adapters/channel"
	"muxfleet/internal/ports"
)

// setupMockSSHSocketPair spins up an SSH+SFTP test server connected via a syscall.Socketpair.
func setupMockSSHSocketPair(t *testing.T) (clientConn net.Conn, clientKeyPEM []byte, hostKeyLine []byte, rootDir string, cleanup func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "sftp_sockpair_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// 1. Generate server host key (ed25519)
	_, serverPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}
	serverSigner, err := ssh.NewSignerFromKey(serverPriv)
	if err != nil {
		t.Fatalf("failed to create server signer: %v", err)
	}
	hostKeyLine = ssh.MarshalAuthorizedKey(serverSigner.PublicKey())

	// 2. Generate client key (ed25519)
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}
	sshClientPub, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("failed to create ssh client pubkey: %v", err)
	}

	privBytes, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	clientKeyPEM = pem.EncodeToMemory(privBytes)

	// 3. Configure SSH Server
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), sshClientPub.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unauthorized key")
		},
	}
	serverConfig.AddHostKey(serverSigner)

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("Socketpair failed: %v", err)
	}

	f0 := os.NewFile(uintptr(fds[0]), "sock0")
	f1 := os.NewFile(uintptr(fds[1]), "sock1")

	serverConn, err := net.FileConn(f0)
	if err != nil {
		t.Fatalf("FileConn sock0 failed: %v", err)
	}
	_ = f0.Close()

	clientConn, err = net.FileConn(f1)
	if err != nil {
		t.Fatalf("FileConn sock1 failed: %v", err)
	}
	_ = f1.Close()

	go handleSSHConn(serverConn, serverConfig, tempDir)

	cleanup = func() {
		clientConn.Close()
		serverConn.Close()
		os.RemoveAll(tempDir)
	}

	return clientConn, clientKeyPEM, hostKeyLine, tempDir, cleanup
}

func handleSSHConn(conn net.Conn, config *ssh.ServerConfig, rootDir string) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		ch, reqs, err := newChan.Accept()
		if err != nil {
			return
		}

		go func(inChannel ssh.Channel, inRequests <-chan *ssh.Request) {
			defer inChannel.Close()

			for req := range inRequests {
				switch req.Type {
				case "subsystem":
					if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
						_ = req.Reply(true, nil)
						server, err := sftp.NewServer(
							inChannel,
							sftp.WithDebug(io.Discard),
						)
						if err != nil {
							return
						}
						_ = server.Serve()
						return
					}
					_ = req.Reply(false, nil)

				case "exec":
					// Echo the command line and stdin back, so tests see exactly what the client sent.
					_ = req.Reply(true, nil)
					cmd := ""
					if len(req.Payload) >= 4 {
						cmd = string(req.Payload[4:])
					}
					in, _ := io.ReadAll(inChannel)
					_, _ = inChannel.Write([]byte("exec: " + cmd + "\nstdin: " + string(in) + "\n"))
					_, _ = inChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					return

				default:
					_ = req.Reply(false, nil)
				}
			}
		}(ch, reqs)
	}
}

func TestSSHClient(t *testing.T) {
	clientConn, clientKey, hostKey, destDir, cleanup := setupMockSSHSocketPair(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := channel.NewSSHClient()
	err := client.Connect(ctx, ports.ChannelTarget{
		Host:          "in-memory",
		Port:          22,
		User:          "root",
		PrivateKey:    clientKey,
		HostPublicKey: hostKey,
		CustomConn:    clientConn,
	})
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	defer client.Close()

	if got := channel.NewSSHClient().PrjPathForTest(); got != ports.PrjPath {
		t.Fatalf("a new client must target %s, got %s (AD-5)", ports.PrjPath, got)
	}

	prjPath := filepath.Join(destDir, "usr", "local", "bin", "prj")
	client.SetPrjPathForTest(prjPath)
	content := []byte("#!/bin/sh\necho 'hello from binary'\n")
	hasher := sha256.New()
	hasher.Write(content)
	expectedSHA := hex.EncodeToString(hasher.Sum(nil))

	t.Run("Poussée SFTP atomique nominale", func(t *testing.T) {
		err := client.PushPrj(ctx, bytes.NewReader(content), expectedSHA)
		if err != nil {
			t.Fatalf("PushPrj failed: %v", err)
		}

		onDisk, err := os.ReadFile(prjPath)
		if err != nil {
			t.Fatalf("prj does not exist on disk: %v", err)
		}
		if !bytes.Equal(onDisk, content) {
			t.Errorf("pushed content differs from source")
		}
		stat, err := os.Stat(prjPath)
		if err != nil {
			t.Fatal(err)
		}
		if stat.Mode().Perm() != 0755 {
			t.Errorf("stat.Mode() = %o, want 0755", stat.Mode().Perm())
		}
		if _, err := os.Stat(prjPath + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("temp file must be renamed away")
		}
	})

	t.Run("Poussée SFTP refusée si empreinte SHA256 incorrecte", func(t *testing.T) {
		badRemotePath := filepath.Join(destDir, "bad-binary")
		client.SetPrjPathForTest(badRemotePath)
		defer client.SetPrjPathForTest(prjPath)
		err := client.PushPrj(ctx, bytes.NewReader(content), "badhash000000000000000000000000000000000000000000000000000000000")
		if err == nil {
			t.Fatal("PushPrj with wrong SHA should fail, got nil")
		}
		if !errors.Is(err, channel.ErrChecksumMismatch) {
			t.Errorf("expected ErrChecksumMismatch, got %v", err)
		}

		// Ensure neither bad-binary nor bad-binary.tmp exist
		if _, err := os.Stat(badRemotePath); !os.IsNotExist(err) {
			t.Errorf("badRemotePath should not exist after failure")
		}
		if _, err := os.Stat(badRemotePath + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("temp file should be cleaned up after failure")
		}
	})

	t.Run("Empreinte distante d'un fichier inexistant", func(t *testing.T) {
		nonExistent := filepath.Join(destDir, "does-not-exist-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		sha, err := client.RemoteSHA256ForTest(ctx, nonExistent)
		if err != nil {
			t.Fatalf("remoteSHA256 failed on non existent: %v", err)
		}
		if sha != "" {
			t.Errorf("expected empty sha, got %q", sha)
		}
	})

	t.Run("RunPrj lance prj par chemin absolu, arguments quotés, stdin vide = /dev/null (AD-5, AD-22)", func(t *testing.T) {
		stdout, stderr, exitCode, err := client.RunPrj(ctx, []string{"version", "it's; rm -rf /"}, nil)
		if err != nil {
			t.Fatalf("RunPrj failed: %v", err)
		}
		if exitCode != 0 {
			t.Errorf("exitCode = %d, want 0 (stderr=%s)", exitCode, string(stderr))
		}
		want := "exec: '" + prjPath + `' 'version' 'it'\''s; rm -rf /' < /dev/null` + "\nstdin: \n"
		if string(stdout) != want {
			t.Errorf("remote command line:\n got %q\nwant %q", string(stdout), want)
		}
	})

	t.Run("RunPrj transmet un document JSON sur stdin", func(t *testing.T) {
		stdout, _, _, err := client.RunPrj(ctx, []string{"system", "apply"}, strings.NewReader(`{"components":["base"]}`))
		if err != nil {
			t.Fatalf("RunPrj failed: %v", err)
		}
		want := "exec: '" + prjPath + "' 'system' 'apply'\nstdin: {\"components\":[\"base\"]}\n"
		if string(stdout) != want {
			t.Errorf("remote side:\n got %q\nwant %q", string(stdout), want)
		}
	})

	t.Run("Exécution avec annulation de contexte", func(t *testing.T) {
		timeoutCtx, cancelTimeout := context.WithCancel(context.Background())
		cancelTimeout() // immediately canceled
		_, _, _, err := client.RunPrj(timeoutCtx, []string{"version"}, nil)
		if err == nil {
			t.Fatal("expected error on cancelled context, got nil")
		}
	})
}

// TestSSHClientHostKeyPinning guards AD-10: the channel only talks to the server whose host key vps expects.
func TestSSHClientHostKeyPinning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("Clé d'hôte attendue absente : refus avant toute connexion", func(t *testing.T) {
		clientConn, clientKey, _, _, cleanup := setupMockSSHSocketPair(t)
		defer cleanup()

		err := channel.NewSSHClient().Connect(ctx, ports.ChannelTarget{
			Host: "in-memory", PrivateKey: clientKey, CustomConn: clientConn,
		})
		if !errors.Is(err, channel.ErrNoHostKey) {
			t.Fatalf("expected ErrNoHostKey, got %v", err)
		}
	})

	t.Run("Clé d'hôte différente : refus de la connexion", func(t *testing.T) {
		clientConn, clientKey, _, _, cleanup := setupMockSSHSocketPair(t)
		defer cleanup()

		otherPub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		otherSSH, err := ssh.NewPublicKey(otherPub)
		if err != nil {
			t.Fatal(err)
		}

		client := channel.NewSSHClient()
		err = client.Connect(ctx, ports.ChannelTarget{
			Host: "in-memory", PrivateKey: clientKey, CustomConn: clientConn,
			HostPublicKey: ssh.MarshalAuthorizedKey(otherSSH),
		})
		if !errors.Is(err, channel.ErrHostKeyMismatch) {
			t.Fatalf("expected ErrHostKeyMismatch, got %v", err)
		}
		if _, _, _, execErr := client.RunPrj(ctx, []string{"version"}, nil); !errors.Is(execErr, channel.ErrNotConnected) {
			t.Errorf("channel must stay unconnected after a host key mismatch, got %v", execErr)
		}
	})
}
