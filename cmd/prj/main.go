package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"muxfleet/internal/domain"
	"muxfleet/internal/version"
	"muxfleet/server/components"
)

const (
	Version       = version.Version
	SchemaVersion = version.SchemaVersion
)

// versionMarker garde la version dans les octets du binaire : vps la lit avant de le pousser (AD-5, R9).
// prj version l'affiche à partir de ce marqueur, ce qui le maintient dans le binaire compilé.
var versionMarker = version.PrjMarker

func markerVersion() string {
	return versionMarker[len(domain.PrjVersionMarkerPrefix) : len(versionMarker)-len(domain.PrjVersionMarkerSuffix)]
}

// skipRootCheckForTest allows unit tests to test commands without root privileges.
var skipRootCheckForTest = false

func main() {
	exitCode := Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}

// ApplyPayload represents the JSON payload passed to prj system apply on stdin.
type ApplyPayload struct {
	Components []string `json:"components"`
	State      string   `json:"state"` // "present" or "absent"
}

// ApplyResult represents the JSON result rendered by prj system apply (AD-22).
type ApplyResult struct {
	SchemaVersion string   `json:"schema_version"`
	Changed       int      `json:"changed"`
	Failures      []string `json:"failures"`
}

// StatusResult represents the JSON result rendered by prj system status --json (AD-2, AD-5).
type StatusResult struct {
	SchemaVersion string             `json:"schema_version"`
	ServerState   domain.ServerState `json:"server_state"`
	DisplayState  string             `json:"display_state"`
	ObservedAt    time.Time          `json:"observed_at"`
	Facts         domain.ServerFacts `json:"facts"`
}

// RecordPayload represents milestones and attestations passed to prj system record (AD-5).
type RecordPayload struct {
	Milestones   []domain.Milestone   `json:"milestones,omitempty"`
	Attestations []domain.Attestation `json:"attestations,omitempty"`
	Proofs       []domain.Proof       `json:"proofs,omitempty"`
	DemoFacts    map[string]any       `json:"demo_facts,omitempty"`
}

// Run executes the prj CLI with injected IO streams.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "muxfleet prj - serveur VPS Ubuntu 26.04")
		fmt.Fprintln(stderr, "Usage: prj <command> [options]")
		fmt.Fprintln(stderr, "Commandes disponibles: version, system")
		return 0
	}

	subcmd := args[0]
	switch subcmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, markerVersion())
		return 0

	case "system":
		return runSystemCommand(args[1:], stdin, stdout, stderr)

	default:
		// User commands (under dev)
		if os.Geteuid() == 0 && !skipRootCheckForTest {
			fmt.Fprintln(stderr, "erreur : prj refuse les commandes utilisateur sous root (AD-5)")
			return 2
		}
		fmt.Fprintf(stderr, "commande inconnue : %s\n", subcmd)
		return 2
	}
}

func runSystemCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// AD-5: prj refuse ses commandes system hors de root
	if os.Geteuid() != 0 && !skipRootCheckForTest {
		fmt.Fprintln(stderr, "erreur : prj refuse ses commandes system hors de root (AD-5)")
		return 2
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: prj system <apply|status|record> [options]")
		return 2
	}

	systemSubcmd := args[0]
	switch systemSubcmd {
	case "status":
		return runSystemStatus(args[1:], stdin, stdout, stderr)
	case "apply":
		return runSystemApply(args[1:], stdin, stdout, stderr)
	case "record":
		return runSystemRecord(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "sous-commande system inconnue : %s\n", systemSubcmd)
		return 2
	}
}

func runSystemStatus(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("system status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonFlag := fs.Bool("json", false, "Sortie au format JSON (AD-19)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	facts, err := loadServerFacts()
	if err != nil {
		fmt.Fprintf(stderr, "erreur : %v\n", err)
		return 1
	}
	serverState := domain.DeriveServerState(facts)

	res := StatusResult{
		SchemaVersion: SchemaVersion,
		ServerState:   serverState,
		DisplayState:  serverState.DisplayLabel(),
		ObservedAt:    time.Now().UTC(),
		Facts:         facts,
	}

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(stderr, "erreur d'encodage JSON: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "État du serveur : %s (%s)\n", res.DisplayState, res.ServerState)
	return 0
}

func runSystemApply(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "erreur : argument inattendu %q : system apply ne lit qu'un document JSON sur stdin (AD-5) ; rien n'a été appliqué\n", args[0])
		return 2
	}

	var payload ApplyPayload

	// One optional JSON document on stdin (AD-5). Only an empty stdin means the story 1 defaults.
	// A document that does not parse, names an unknown field, asks for no component, asks for a
	// state other than present, or names an unknown component is refused before anything runs.
	var stdinData []byte
	if stdin != nil {
		var err error
		if stdinData, err = io.ReadAll(stdin); err != nil {
			fmt.Fprintf(stderr, "erreur de lecture de stdin: %v\n", err)
			return 2
		}
	}
	if len(bytes.TrimSpace(stdinData)) == 0 {
		payload.Components = []string{"base", "prj", "reboot"}
	} else {
		if err := decodeStrict(stdinData, &payload); err != nil {
			fmt.Fprintf(stderr, "erreur : document JSON invalide sur stdin pour system apply : %v ; rien n'a été appliqué\n", err)
			return 2
		}
		if len(payload.Components) == 0 {
			fmt.Fprintln(stderr, "erreur : le document ne demande aucun composant (components vide) ; rien n'a été appliqué")
			return 2
		}
	}
	if payload.State != "" && payload.State != "present" {
		fmt.Fprintf(stderr, "erreur : état %q non pris en charge, seul \"present\" existe dans cette version ; rien n'a été appliqué\n", payload.State)
		return 2
	}

	scripts := make([][]byte, len(payload.Components))
	var unknown []string
	for i, comp := range payload.Components {
		script, err := componentScript(comp)
		if err != nil {
			unknown = append(unknown, fmt.Sprintf("%s (%v)", comp, err))
			continue
		}
		scripts[i] = script
	}
	if len(unknown) > 0 {
		fmt.Fprintf(stderr, "erreur : composant inconnu : %s ; rien n'a été appliqué\n", strings.Join(unknown, ", "))
		return 2
	}

	totalChanged := 0
	var allFailures []string

	for i, comp := range payload.Components {
		ctx, cancel := context.WithTimeout(context.Background(), componentTimeout)
		cmd := exec.CommandContext(ctx, "bash", "-c", string(scripts[i]))
		cmd.Stdin = nil // < /dev/null (AD-22)
		var compStderr bytes.Buffer
		cmd.Stderr = &compStderr
		// When the deadline kills bash, a child (apt, dpkg) may still hold the output pipes:
		// stop waiting for them after componentWaitDelay. The child itself is left to finish,
		// since killing dpkg midway would break the package database.
		cmd.WaitDelay = componentWaitDelay

		outputBytes, cmdErr := cmd.Output()
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			cmdErr = fmt.Errorf("délai de %s dépassé, script interrompu (%v)", componentTimeout, cmdErr)
		}

		// Component JSON output: {"component": "...", "changed": N, "failures": [...]}
		var compResult struct {
			Changed  int      `json:"changed"`
			Failures []string `json:"failures"`
		}
		parseErr := json.Unmarshal(outputBytes, &compResult)

		if cmdErr != nil {
			// Keep what the component reported before failing, and say what is lost otherwise.
			msg := fmt.Sprintf("%s: exécution échouée: %v", comp, cmdErr)
			if parseErr == nil {
				totalChanged += compResult.Changed
				allFailures = append(allFailures, compResult.Failures...)
			} else {
				msg += " ; pas de compte rendu, ses changements ne sont pas comptés"
			}
			if tail := tailText(compStderr.String(), 3, 400); tail != "" {
				msg += " ; stderr : " + tail
			}
			allFailures = append(allFailures, msg)
			continue
		}

		if parseErr != nil {
			allFailures = append(allFailures, fmt.Sprintf("%s: sortie JSON invalide (%v): %s", comp, parseErr, string(outputBytes)))
		} else {
			totalChanged += compResult.Changed
			allFailures = append(allFailures, compResult.Failures...)
		}
	}

	result := newApplyResult(totalChanged, allFailures)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)

	if len(allFailures) > 0 {
		return 1
	}
	return 0
}

// newApplyResult keeps failures a JSON array, never null (--json contract, AD-19).
func newApplyResult(changed int, failures []string) ApplyResult {
	if failures == nil {
		failures = []string{}
	}
	return ApplyResult{
		SchemaVersion: SchemaVersion,
		Changed:       changed,
		Failures:      failures,
	}
}

func runSystemRecord(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "erreur : argument inattendu %q : system record ne lit qu'un document JSON sur stdin (AD-5) ; rien n'a été écrit\n", args[0])
		return 2
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	data, err := io.ReadAll(stdin)
	if err != nil || len(data) == 0 {
		fmt.Fprintln(stderr, "erreur : un document JSON est requis sur stdin pour system record (AD-5)")
		return 2
	}

	var payload RecordPayload
	if err := decodeStrict(data, &payload); err != nil {
		fmt.Fprintf(stderr, "erreur : document JSON invalide sur stdin pour system record : %v ; rien n'a été écrit\n", err)
		return 2
	}
	if err := validateRecordPayload(payload); err != nil {
		fmt.Fprintf(stderr, "erreur : %v ; rien n'a été écrit\n", err)
		return 2
	}

	statusDir := getStatusDir()
	if err := os.MkdirAll(statusDir, 0700); err != nil {
		fmt.Fprintf(stderr, "erreur de création du dossier status %s: %v\n", statusDir, err)
		return 1
	}

	// Two records at once would both read the old file and the last rename would win, losing
	// a write and breaking "written once" (AD-2): read, merge and write under one lock.
	unlock, err := lockRecords(statusDir)
	if err != nil {
		fmt.Fprintf(stderr, "erreur : verrou du fichier d'état impossible : %v ; rien n'a été écrit\n", err)
		return 1
	}
	defer unlock()

	// Persist recorded payload. An unreadable record is never replaced: it may hold activated_at (AD-2).
	recordFile := filepath.Join(statusDir, recordsFileName)
	existing, err := readRecords(recordFile)
	if err != nil {
		fmt.Fprintf(stderr, "erreur : %v ; rien n'a été écrit\n", err)
		return 1
	}

	merged, already := mergeRecords(existing, payload)

	savedBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "erreur de sérialisation: %v\n", err)
		return 1
	}

	if err := writeFileAtomic(recordFile, savedBytes); err != nil {
		fmt.Fprintf(stderr, "erreur d'écriture dans %s: %v\n", recordFile, err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	if err := enc.Encode(RecordResult{SchemaVersion: SchemaVersion, Recorded: true, AlreadyRecorded: already}); err != nil {
		fmt.Fprintf(stderr, "erreur d'encodage JSON: %v\n", err)
		return 1
	}
	return 0
}

// RecordResult is what prj system record prints. AlreadyRecorded names the milestones and
// attestations that were already present: their first date is kept (AD-2, written once).
type RecordResult struct {
	SchemaVersion   string   `json:"schema_version"`
	Recorded        bool     `json:"recorded"`
	AlreadyRecorded []string `json:"already_recorded"`
}

// validateRecordPayload refuses what AD-2 does not know: in v1, one milestone (activated_at)
// and one attestation (recovery_kit_verified), each with its date and at most once per document;
// a proof carries its fact, its source and its time. An empty document records nothing.
func validateRecordPayload(p RecordPayload) error {
	if len(p.Milestones) == 0 && len(p.Attestations) == 0 && len(p.Proofs) == 0 && len(p.DemoFacts) == 0 {
		return errors.New("document vide : aucun jalon, attestation, preuve ni fait de démo")
	}
	seen := map[string]bool{}
	for _, m := range p.Milestones {
		if !domain.IsKnownMilestone(m.Name) {
			return fmt.Errorf("jalon inconnu %q : seul %q existe (AD-2)", m.Name, domain.MilestoneActivatedAt)
		}
		if m.ReachedAt.IsZero() {
			return fmt.Errorf("jalon %q sans date reached_at", m.Name)
		}
		if seen[m.Name] {
			return fmt.Errorf("jalon %q en double dans le document", m.Name)
		}
		seen[m.Name] = true
	}
	for _, a := range p.Attestations {
		if !domain.IsKnownAttestation(a.Type) {
			return fmt.Errorf("attestation inconnue %q : seule %q existe (AD-2)", a.Type, domain.AttestationRecoveryKitVerified)
		}
		if a.AttestedAt.IsZero() {
			return fmt.Errorf("attestation %q sans date attested_at", a.Type)
		}
		if seen[a.Type] {
			return fmt.Errorf("attestation %q en double dans le document", a.Type)
		}
		seen[a.Type] = true
	}
	for i, pr := range p.Proofs {
		if pr.Fact == "" || pr.Source == "" || pr.ObservedAt.IsZero() {
			return fmt.Errorf("preuve %d incomplète : il faut fact, source et observed_at (AD-2)", i+1)
		}
	}
	return nil
}

// lockRecords takes an exclusive lock on a lock file next to the record file, and returns its release.
func lockRecords(statusDir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(statusDir, "."+recordsFileName+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// mergeRecords adds p to existing. A milestone or an attestation is written once (AD-2):
// recording it again keeps the first one and names it in the returned list, so vps can
// retry a record without changing anything. Proofs and demo facts are added as before.
func mergeRecords(existing, p RecordPayload) (RecordPayload, []string) {
	already := []string{}
	for _, m := range p.Milestones {
		if (domain.ServerFacts{Milestones: existing.Milestones}).HasMilestone(m.Name) {
			already = append(already, m.Name)
			continue
		}
		existing.Milestones = append(existing.Milestones, m)
	}
	for _, a := range p.Attestations {
		if (domain.ServerFacts{Attestations: existing.Attestations}).HasAttestation(a.Type) {
			already = append(already, a.Type)
			continue
		}
		existing.Attestations = append(existing.Attestations, a)
	}
	existing.Proofs = append(existing.Proofs, p.Proofs...)
	if p.DemoFacts != nil {
		if existing.DemoFacts == nil {
			existing.DemoFacts = make(map[string]any)
		}
		for k, v := range p.DemoFacts {
			existing.DemoFacts[k] = v
		}
	}
	return existing, already
}

// componentScript returns a component's embedded script; tests replace it to inject failures.
var componentScript = components.GetComponentScript

// componentTimeout bounds one component run; componentWaitDelay bounds the wait for its pipes
// once it is killed. Tests shorten both.
var (
	componentTimeout   = 2 * time.Minute
	componentWaitDelay = 5 * time.Second
)

// decodeStrict reads exactly one JSON document with no unknown field (AD-5).
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("un seul document JSON est attendu")
	}
	return nil
}

// tailText keeps the last n non-empty lines of s on one line, at most max bytes.
func tailText(s string, n, max int) string {
	var kept []string
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			kept = append([]string{l}, kept...)
		}
	}
	out := strings.Join(kept, " | ")
	if len(out) > max {
		start := len(out) - max
		for start < len(out) && !utf8.RuneStart(out[start]) {
			start++
		}
		out = "…" + out[start:]
	}
	return out
}

func getStatusDir() string {
	if custom := os.Getenv("MUXFLEET_STATUS_DIR"); custom != "" {
		return custom
	}
	return "/var/lib/muxfleet/status"
}

// recordsFileName holds the attestations and milestones written by prj system record (AD-2).
const recordsFileName = "system_records.json"

// loadServerFacts gathers the facts prj can see from the server itself, which therefore exists and answers.
// The record file is the only source of the activated_at milestone (AD-2).
func loadServerFacts() (domain.ServerFacts, error) {
	records, err := readRecords(filepath.Join(getStatusDir(), recordsFileName))
	if err != nil {
		return domain.ServerFacts{}, err
	}
	return domain.ServerFacts{
		ServerReachable: true,
		HasServer:       true,
		Milestones:      records.Milestones,
		Attestations:    records.Attestations,
		Proofs:          records.Proofs,
	}, nil
}

// readRecords reads the record file. A missing file means nothing was recorded yet.
// An unreadable or corrupt file is an error, never an empty record: an activated server
// would otherwise show as a demo and be offered « Tout éteindre » (AD-2, arch. state diagram).
func readRecords(path string) (RecordPayload, error) {
	var records RecordPayload
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return records, fmt.Errorf("lecture de %s échouée, l'état du serveur ne peut pas être dérivé: %w", path, err)
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return records, fmt.Errorf("%s est illisible (JSON invalide), l'état du serveur ne peut pas être dérivé: %w", path, err)
	}
	return records, nil
}

// writeFileAtomic writes data to path with mode 0600 through a temporary file and a rename,
// so an interruption leaves either the old record or the new one, never a truncated file.
// The directory is synced after the rename, so that once record has answered, a power loss
// cannot bring the old record back (and with it lose activated_at).
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("synchronisation du dossier %s échouée: %w", filepath.Dir(path), err)
	}
	return nil
}
