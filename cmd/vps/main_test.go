package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"muxfleet/internal/domain"
	"muxfleet/internal/ports"
)

type mockChannel struct {
	remoteVersion string
	remoteExists  bool
	executeErr    error
	ranArgs       [][]string
	pushedContent []byte
	pushedSHA     string
}

// fakePrj returns bytes carrying the prj version marker, as a compiled prj does.
func fakePrj(v string) []byte {
	return []byte("\x7fELF...noise..." + domain.PrjVersionMarkerPrefix + v + domain.PrjVersionMarkerSuffix + "...noise\n")
}

func (m *mockChannel) Connect(ctx context.Context, target ports.ChannelTarget) error {
	return nil
}

func (m *mockChannel) PushPrj(ctx context.Context, src io.Reader, expectedSHA256 string) error {
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	m.pushedContent = data
	m.pushedSHA = expectedSHA256
	return nil
}

func (m *mockChannel) RunPrj(ctx context.Context, args []string, stdin io.Reader) (stdout, stderr []byte, exitCode int, err error) {
	m.ranArgs = append(m.ranArgs, args)
	if m.executeErr != nil {
		return nil, nil, 1, m.executeErr
	}
	if len(args) == 1 && args[0] == "version" {
		if !m.remoteExists {
			return nil, []byte("command not found"), 127, nil
		}
		return []byte(m.remoteVersion + "\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}

func (m *mockChannel) Close() error {
	return nil
}

func TestPushPrj(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sampleBinary := fakePrj("0.1.0")
	hasher := sha256.New()
	hasher.Write(sampleBinary)
	expectedSHA := hex.EncodeToString(hasher.Sum(nil))

	t.Run("Poussée nominale quand version distante absente", func(t *testing.T) {
		mock := &mockChannel{
			remoteExists: false,
		}

		pushed, err := PushPrj(ctx, mock, sampleBinary)
		if err != nil {
			t.Fatalf("PushPrj failed: %v", err)
		}
		if pushed != "0.1.0" {
			t.Errorf("pushed version = %q, want 0.1.0", pushed)
		}

		if len(mock.ranArgs) != 1 || strings.Join(mock.ranArgs[0], " ") != "version" {
			t.Errorf("ran on server = %v, want only prj version", mock.ranArgs)
		}
		if mock.pushedSHA != expectedSHA {
			t.Errorf("pushedSHA = %q, want %q", mock.pushedSHA, expectedSHA)
		}
		if !bytes.Equal(mock.pushedContent, sampleBinary) {
			t.Errorf("pushedContent does not match sampleBinary")
		}
	})

	t.Run("Poussée nominale quand version distante plus ancienne", func(t *testing.T) {
		mock := &mockChannel{
			remoteExists:  true,
			remoteVersion: "0.0.9",
		}

		if _, err := PushPrj(ctx, mock, sampleBinary); err != nil {
			t.Fatalf("PushPrj failed: %v", err)
		}

		if !bytes.Equal(mock.pushedContent, sampleBinary) {
			t.Errorf("pushedContent does not match sampleBinary")
		}
	})

	t.Run("Refus de rétrogradation quand version distante plus récente (R9)", func(t *testing.T) {
		mock := &mockChannel{
			remoteExists:  true,
			remoteVersion: "0.2.0",
		}

		_, err := PushPrj(ctx, mock, sampleBinary)
		if err == nil {
			t.Fatal("PushPrj should fail on downgrade, got nil")
		}
		if !errors.Is(err, domain.ErrDowngradeRefused) {
			t.Errorf("expected ErrDowngradeRefused, got %v", err)
		}

		// Ensure nothing was pushed
		if len(mock.pushedContent) > 0 {
			t.Errorf("expected no content to be pushed, got %d bytes", len(mock.pushedContent))
		}
	})

	t.Run("Refus d'un binaire plus ancien que le serveur, quelle que soit la version de vps (R9)", func(t *testing.T) {
		mock := &mockChannel{
			remoteExists:  true,
			remoteVersion: "0.1.0",
		}

		_, err := PushPrj(ctx, mock, fakePrj("0.0.9"))
		if !errors.Is(err, domain.ErrDowngradeRefused) {
			t.Fatalf("expected ErrDowngradeRefused, got %v", err)
		}
		if len(mock.pushedContent) > 0 {
			t.Errorf("expected no content to be pushed, got %d bytes", len(mock.pushedContent))
		}
	})

	t.Run("La version annoncée est celle du binaire poussé", func(t *testing.T) {
		mock := &mockChannel{
			remoteExists:  true,
			remoteVersion: "0.1.0",
		}

		pushed, err := PushPrj(ctx, mock, fakePrj("0.2.0"))
		if err != nil {
			t.Fatalf("PushPrj failed: %v", err)
		}
		if pushed != "0.2.0" {
			t.Errorf("pushed version = %q, want 0.2.0", pushed)
		}
	})

	t.Run("Refus d'un binaire sans marqueur de version", func(t *testing.T) {
		mock := &mockChannel{}

		_, err := PushPrj(ctx, mock, []byte("#!/bin/sh\necho 'prj binary'\n"))
		if !errors.Is(err, domain.ErrNoVersionMarker) {
			t.Fatalf("expected ErrNoVersionMarker, got %v", err)
		}
		if len(mock.pushedContent) > 0 {
			t.Errorf("expected no content to be pushed, got %d bytes", len(mock.pushedContent))
		}
	})

	t.Run("Refus si la version distante ne peut pas être lue faute de canal", func(t *testing.T) {
		transportErr := errors.New("connexion perdue")
		mock := &mockChannel{executeErr: transportErr}

		_, err := PushPrj(ctx, mock, sampleBinary)
		if !errors.Is(err, transportErr) {
			t.Fatalf("expected transport error, got %v", err)
		}
		if len(mock.pushedContent) > 0 {
			t.Errorf("expected no content to be pushed, got %d bytes", len(mock.pushedContent))
		}
	})
}

// embeddedPrj returns the prj binary embedded in vps. It is generated, never committed:
// `go generate ./cmd/vps` builds it, as CI does before the tests.
func embeddedPrj(t *testing.T) []byte {
	t.Helper()
	data, err := embeddedFS.ReadFile("embed/prj-linux-amd64")
	if err != nil {
		t.Fatalf("embedded prj missing (%v): run go generate ./cmd/vps before go test", err)
	}
	return data
}

// TestEmbeddedPrjCarriesOwnVersion guards AD-5: vps embeds prj binaries of its own version.
// It fails when the generated binary is stale: run go generate ./cmd/vps again.
func TestEmbeddedPrjCarriesOwnVersion(t *testing.T) {
	data := embeddedPrj(t)
	got, err := domain.PrjVersionFromBinary(data)
	if err != nil {
		t.Fatalf("embedded prj version unreadable: %v", err)
	}
	if got != Version {
		t.Errorf("embedded prj version = %q, want vps version %q", got, Version)
	}
}

// TestEmbeddedPrjHasNoLocalPaths keeps the builder's file paths out of the binary that the public
// repository ships: built without -trimpath, it would carry this checkout's absolute path.
func TestEmbeddedPrjHasNoLocalPaths(t *testing.T) {
	data := embeddedPrj(t)
	wd, err := os.Getwd() // the package directory, <module root>/cmd/vps
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(wd))
	if bytes.Contains(data, []byte(moduleRoot)) {
		t.Errorf("embedded prj contains the local path %s: regenerate it with go generate ./cmd/vps (-trimpath)", moduleRoot)
	}
}

// TestEmbeddedPrjBuildSettings checks that the embedded prj was built by `go generate ./cmd/vps`:
// a static Linux amd64 binary without the builder's file paths.
func TestEmbeddedPrjBuildSettings(t *testing.T) {
	data := embeddedPrj(t)
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("embedded prj build info unreadable: %v", err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	for key, want := range map[string]string{"-trimpath": "true", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64"} {
		if settings[key] != want {
			t.Errorf("embedded prj built with %s=%q, want %q: regenerate it with go generate ./cmd/vps", key, settings[key], want)
		}
	}
}

func TestVpsRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"version"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Errorf("version exitCode = %d, want 0", code)
	}
	if strings.TrimSpace(stdout.String()) != Version {
		t.Errorf("stdout = %q, want %q", strings.TrimSpace(stdout.String()), Version)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"unknown-command"}, nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown-command exitCode = %d, want 2", code)
	}
}

// TestPushPrjHasNoRemotePath guards AD-5: prj only ever lands at /usr/local/bin/prj.
func TestPushPrjHasNoRemotePath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"push-prj", "--remote-path", "/tmp/prj", "--host-key", "/dev/null"}, nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("--remote-path must be an unknown flag (code 2), got %d", code)
	}
	if ports.PrjPath != "/usr/local/bin/prj" {
		t.Errorf("ports.PrjPath = %q, want /usr/local/bin/prj (AD-5)", ports.PrjPath)
	}
}

func TestVpsServerMarkerCheck(t *testing.T) {
	tempDir := t.TempDir()
	origMarker := serverMarkerPath
	defer func() { serverMarkerPath = origMarker }()

	serverMarkerPath = tempDir // exists!

	var stdout, stderr bytes.Buffer
	code := Run([]string{"version"}, nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exitCode 2 when server marker exists, got %d", code)
	}
	if !strings.Contains(stderr.String(), "vps refuse de s'exécuter sur un serveur muxfleet") {
		t.Errorf("expected server marker error message, got %q", stderr.String())
	}
}
