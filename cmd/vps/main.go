package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"muxfleet/internal/adapters/channel"
	"muxfleet/internal/domain"
	"muxfleet/internal/ports"
	"muxfleet/internal/version"
)

const (
	Version       = version.Version
	SchemaVersion = version.SchemaVersion
)

// The Linux prj binary that vps pushes is generated, never committed: run `go generate ./cmd/vps`
// before building vps (README). embed/README.md keeps the pattern valid in a fresh checkout.
//
//go:generate env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o embed/prj-linux-amd64 ../prj
//go:embed embed/*
var embeddedFS embed.FS

// serverMarkerPath is checked at startup to prevent running on a muxfleet server (AD-5).
var serverMarkerPath = "/etc/muxfleet"

func main() {
	exitCode := Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}

// Run executes the vps CLI with injected IO streams.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// AD-5: vps refuse de s'exécuter sur un serveur muxfleet
	if _, err := os.Stat(serverMarkerPath); err == nil {
		fmt.Fprintln(stderr, "erreur : vps refuse de s'exécuter sur un serveur muxfleet (/etc/muxfleet présent) (AD-5)")
		return 2
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "muxfleet vps - poste administrateur")
		fmt.Fprintln(stderr, "Usage: vps <command> [options]")
		fmt.Fprintln(stderr, "Commandes disponibles: version, push-prj, init")
		return 0
	}

	subcmd := args[0]
	switch subcmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, Version)
		return 0

	case "push-prj":
		return runPushPrj(args[1:], stdin, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "commande inconnue : %s\n", subcmd)
		return 2
	}
}

func runPushPrj(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("push-prj", flag.ContinueOnError)
	fs.SetOutput(stderr)

	host := fs.String("host", "10.83.0.1", "Hôte ou IP WireGuard du serveur distant (AD-4)")
	port := fs.Int("port", 22, "Port SSH d'administration")
	user := fs.String("user", "root", "Utilisateur d'administration (AD-5, AD-11)")
	keyFile := fs.String("key-file", "", "Chemin vers la clé privée SSH d'administration")
	hostKeyFile := fs.String("host-key", "", "Fichier de la clé d'hôte SSH attendue du serveur, format authorized_keys (obligatoire, AD-10)")
	prjBin := fs.String("prj-bin", "", "Chemin optionnel vers le binaire prj (défaut: binaire embarqué)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	var prjBytes []byte
	var err error

	if *prjBin != "" {
		prjBytes, err = os.ReadFile(*prjBin)
		if err != nil {
			fmt.Fprintf(stderr, "erreur de lecture du binaire prj spécifié (%s): %v\n", *prjBin, err)
			return 2
		}
	} else {
		prjBytes, err = embeddedFS.ReadFile("embed/prj-linux-amd64")
		if err != nil {
			fmt.Fprintf(stderr, "erreur : ce vps a été compilé sans binaire prj embarqué (%v) ; lancez go generate ./cmd/vps puis recompilez vps, ou passez --prj-bin\n", err)
			return 1
		}
	}

	if *hostKeyFile == "" {
		fmt.Fprintln(stderr, "erreur : --host-key est obligatoire : vps refuse un serveur dont il ne connaît pas la clé d'hôte (AD-10)")
		return 2
	}
	hostKeyBytes, err := os.ReadFile(*hostKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "erreur de lecture de la clé d'hôte attendue (%s): %v\n", *hostKeyFile, err)
		return 2
	}

	var privKeyBytes []byte
	if *keyFile != "" {
		privKeyBytes, err = os.ReadFile(*keyFile)
		if err != nil {
			fmt.Fprintf(stderr, "erreur de lecture de la clé privée SSH (%s): %v\n", *keyFile, err)
			return 2
		}
	}

	ch := channel.NewSSHClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := ports.ChannelTarget{
		Host:          *host,
		Port:          *port,
		User:          *user,
		PrivateKey:    privKeyBytes,
		HostPublicKey: hostKeyBytes,
	}

	if err := ch.Connect(ctx, target); err != nil {
		fmt.Fprintf(stderr, "erreur de connexion SSH/SFTP vers %s:%d: %v\n", *host, *port, err)
		return 1
	}
	defer ch.Close()

	pushedVersion, err := PushPrj(ctx, ch, prjBytes)
	if err != nil {
		fmt.Fprintf(stderr, "erreur de déploiement de prj: %v\n", err)
		return 1
	}

	hasher := sha256.New()
	hasher.Write(prjBytes)
	sha := hex.EncodeToString(hasher.Sum(nil))

	fmt.Fprintf(stdout, "✓ prj v%s posé avec succès sur %s (SHA256: %s)\n", pushedVersion, ports.PrjPath, sha)
	return 0
}

// PushPrj coordinates version checking, downgrade refusal and atomic SFTP upload (AD-5, R9).
// The version compared is the one carried by prjBytes itself, never vps's own version,
// so a binary given with --prj-bin cannot downgrade the server. Returns the pushed version.
// prj always lands at ports.PrjPath: AD-5 allows no other path.
func PushPrj(ctx context.Context, ch ports.ServerChannel, prjBytes []byte) (string, error) {
	// 1. Read the version carried by the binary about to be pushed
	pushedVersion, err := domain.PrjVersionFromBinary(prjBytes)
	if err != nil {
		return "", fmt.Errorf("version du binaire prj illisible: %w", err)
	}

	// 2. Check remote prj version; a transport failure must not pass for an absent prj
	stdout, _, exitCode, err := ch.RunPrj(ctx, []string{"version"}, nil)
	if err != nil {
		return "", fmt.Errorf("lecture de la version distante de prj échouée: %w", err)
	}

	remoteVersion := ""
	if exitCode == 0 {
		remoteVersion = strings.TrimSpace(string(stdout))
	}

	// 3. Check upgrade allowed (refuse downgrade) (R9)
	if err := domain.CheckUpgradeAllowed(remoteVersion, pushedVersion); err != nil {
		return "", err
	}

	// 4. Compute expected SHA256 of prjBytes
	hasher := sha256.New()
	hasher.Write(prjBytes)
	expectedSHA := hex.EncodeToString(hasher.Sum(nil))

	// 5. Push atomically via SFTP to ports.PrjPath (chmod 0755)
	if err := ch.PushPrj(ctx, bytes.NewReader(prjBytes), expectedSHA); err != nil {
		return "", fmt.Errorf("poussée atomique de prj échouée: %w", err)
	}

	return pushedVersion, nil
}
