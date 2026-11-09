package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPrjRun(t *testing.T) {
	// Bypass root checks during unit tests via package variable (AD-5, AD-10)
	origSkip := skipRootCheckForTest
	skipRootCheckForTest = true
	defer func() { skipRootCheckForTest = origSkip }()

	tempDir := t.TempDir()
	t.Setenv("MUXFLEET_STATUS_DIR", tempDir)

	t.Run("prj version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"version"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Errorf("exitCode = %d, want 0", code)
		}
		if strings.TrimSpace(stdout.String()) != Version {
			t.Errorf("stdout = %q, want %q", strings.TrimSpace(stdout.String()), Version)
		}
	})

	t.Run("prj system status --json nominal (démo sans jalon)", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"system", "status", "--json"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exitCode = %d, want 0 (stderr=%s)", code, stderr.String())
		}

		var res StatusResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if res.ServerState != "demo" {
			t.Errorf("ServerState = %q, want 'demo'", res.ServerState)
		}
		if res.DisplayState != "Démo" {
			t.Errorf("DisplayState = %q, want 'Démo'", res.DisplayState)
		}
	})

	t.Run("prj system record permissions 0600 et transition état actif", func(t *testing.T) {
		recordJSON := `{"milestones": [{"name": "activated_at", "reached_at": "2026-11-06T12:00:00Z"}]}`
		var stdout, stderr bytes.Buffer

		code := Run([]string{"system", "record"}, strings.NewReader(recordJSON), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("system record exitCode = %d, want 0 (stderr=%s)", code, stderr.String())
		}

		// Verify system_records.json has permissions 0600 (AD-10, AD-13)
		recPath := filepath.Join(tempDir, "system_records.json")
		fi, err := os.Stat(recPath)
		if err != nil {
			t.Fatalf("failed to stat system_records.json: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Errorf("system_records.json mode = %o, want 0600", fi.Mode().Perm())
		}

		// Re-check status: must derive 'active' (Actif)
		stdout.Reset()
		stderr.Reset()
		code = Run([]string{"system", "status", "--json"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("system status exitCode = %d, want 0 (stderr=%s)", code, stderr.String())
		}

		var res StatusResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if res.ServerState != "active" {
			t.Errorf("ServerState = %q, want 'active'", res.ServerState)
		}
		if res.DisplayState != "Actif" {
			t.Errorf("DisplayState = %q, want 'Actif'", res.DisplayState)
		}
	})

	t.Run("system_records.json corrompu : status refuse de dériver un état (F11)", func(t *testing.T) {
		isolatedDir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", isolatedDir)
		recPath := filepath.Join(isolatedDir, "system_records.json")
		if err := os.WriteFile(recPath, []byte(`{"milestones": [{"name": "activated_at"`), 0600); err != nil {
			t.Fatal(err)
		}

		for _, args := range [][]string{{"system", "status", "--json"}, {"system", "status"}} {
			var stdout, stderr bytes.Buffer
			code := Run(args, nil, &stdout, &stderr)
			if code != 1 {
				t.Errorf("%v: exitCode = %d, want 1", args, code)
			}
			if stdout.Len() != 0 {
				t.Errorf("%v: stdout must stay empty, a state would be a guess: %q", args, stdout.String())
			}
			if !strings.Contains(stderr.String(), recPath) {
				t.Errorf("%v: stderr must name the file, got %q", args, stderr.String())
			}
		}
	})

	t.Run("system_records.json corrompu : record refuse de l'écraser (F11)", func(t *testing.T) {
		isolatedDir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", isolatedDir)
		recPath := filepath.Join(isolatedDir, "system_records.json")
		corrupt := []byte(`{"milestones": [{"name": "activated_at"`)
		if err := os.WriteFile(recPath, corrupt, 0600); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := Run([]string{"system", "record"}, strings.NewReader(`{"proofs": [{"fact": "x", "source": "y", "observed_at": "2026-11-09T09:00:00Z"}]}`), &stdout, &stderr)
		if code != 1 {
			t.Fatalf("exitCode = %d, want 1 (stderr=%s)", code, stderr.String())
		}
		got, err := os.ReadFile(recPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, corrupt) {
			t.Errorf("record must leave the unreadable file untouched, it may hold activated_at; got %q", got)
		}
	})

	t.Run("record cumule les enregistrements sans fichier temporaire restant", func(t *testing.T) {
		isolatedDir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", isolatedDir)

		for _, rec := range []string{
			`{"attestations": [{"type": "recovery_kit_verified", "attested_at": "2026-11-06T11:00:00Z"}]}`,
			`{"milestones": [{"name": "activated_at", "reached_at": "2026-11-06T12:00:00Z"}]}`,
		} {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"system", "record"}, strings.NewReader(rec), &stdout, &stderr); code != 0 {
				t.Fatalf("record exitCode = %d (stderr=%s)", code, stderr.String())
			}
		}

		entries, err := os.ReadDir(isolatedDir)
		if err != nil {
			t.Fatal(err)
		}
		var kept []os.DirEntry
		for _, e := range entries {
			if e.Name() != ".system_records.json.lock" {
				kept = append(kept, e)
			}
		}
		entries = kept
		if len(entries) != 1 || entries[0].Name() != "system_records.json" {
			names := []string{}
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("status dir = %v, want only system_records.json", names)
		}
		if fi, err := os.Stat(filepath.Join(isolatedDir, "system_records.json")); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("system_records.json must be 0600, got %v (err=%v)", fi.Mode().Perm(), err)
		}

		var stdout, stderr bytes.Buffer
		if code := Run([]string{"system", "status", "--json"}, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("status exitCode = %d (stderr=%s)", code, stderr.String())
		}
		var res StatusResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.ServerState != "active" || len(res.Facts.Attestations) != 1 || len(res.Facts.Milestones) != 1 {
			t.Errorf("state = %q, attestations = %d, milestones = %d; want active, 1, 1", res.ServerState, len(res.Facts.Attestations), len(res.Facts.Milestones))
		}
	})

	t.Run("un fichier milestones/activated_at seul ne rend pas le serveur actif", func(t *testing.T) {
		isolatedDir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", isolatedDir)
		if err := os.MkdirAll(filepath.Join(isolatedDir, "milestones"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(isolatedDir, "milestones", "activated_at"), []byte("activated"), 0600); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		if code := Run([]string{"system", "status", "--json"}, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("status exitCode = %d (stderr=%s)", code, stderr.String())
		}
		var res StatusResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.ServerState != "demo" {
			t.Errorf("ServerState = %q, want demo: system_records.json is the only source of activated_at", res.ServerState)
		}
	})

	t.Run("prj system apply sortie JSON", func(t *testing.T) {
		fakeComponents(t, nil) // never run the real scripts on the developer machine
		var stdout, stderr bytes.Buffer
		applyJSON := `{"components": ["base", "prj", "reboot"]}`

		code := Run([]string{"system", "apply"}, strings.NewReader(applyJSON), &stdout, &stderr)
		if code != 0 {
			t.Logf("system apply returned code %d (warnings on non-root/non-linux environment expected): %s", code, stderr.String())
		}

		var result ApplyResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("failed to parse apply result JSON: %v", err)
		}
		if result.SchemaVersion != SchemaVersion {
			t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, SchemaVersion)
		}
	})
}

func TestPrjRootCheck(t *testing.T) {
	origSkip := skipRootCheckForTest
	skipRootCheckForTest = false
	defer func() { skipRootCheckForTest = origSkip }()

	var stdout, stderr bytes.Buffer

	// On non-root (macOS user), system commands must be refused with code 2 (AD-5)
	if os.Geteuid() != 0 {
		code := Run([]string{"system", "status"}, nil, &stdout, &stderr)
		if code != 2 {
			t.Errorf("exitCode = %d, want 2 (non-root running system command)", code)
		}
		if !strings.Contains(stderr.String(), "refuse ses commandes system hors de root") {
			t.Errorf("expected root refusal error message, got %q", stderr.String())
		}
	}
}

func TestApplyResultFailuresNeverNull(t *testing.T) {
	out, err := json.Marshal(newApplyResult(0, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"failures":[]`) {
		t.Errorf("apply result = %s, want failures as [] (plan I/O matrix)", out)
	}
}

// fakeComponents replaces the embedded scripts with harmless ones. The known components
// (base, prj, reboot) append their name to a run log when they actually run; the returned
// function reads that log. Unknown names are refused, as by the real lookup.
func fakeComponents(t *testing.T, scripts map[string]string) func() []string {
	t.Helper()
	runLog := filepath.Join(t.TempDir(), "runs")
	orig := componentScript
	componentScript = func(name string) ([]byte, error) {
		if s, ok := scripts[name]; ok {
			return []byte(s), nil
		}
		switch name {
		case "base", "prj", "reboot":
			return []byte(`echo ` + name + ` >> '` + runLog + `'; echo '{"component": "` + name + `", "changed": 0, "failures": []}'`), nil
		}
		return nil, errors.New("composant inconnu")
	}
	t.Cleanup(func() { componentScript = orig })
	return func() []string {
		data, err := os.ReadFile(runLog)
		if err != nil {
			return nil
		}
		return strings.Fields(string(data))
	}
}

// TestApplyRefusesUnreadableRequest guards AD-5 (F19): a request that does not parse never runs the defaults.
func TestApplyRefusesUnreadableRequest(t *testing.T) {
	origSkip := skipRootCheckForTest
	skipRootCheckForTest = true
	defer func() { skipRootCheckForTest = origSkip }()

	for name, doc := range map[string]string{
		"JSON tronqué":                           `{"components": ["reboot"`,
		"clé mal orthographiée":                  `{"component": ["reboot"]}`,
		"deux documents à la fois":               `{"components": ["reboot"]} {"components": ["base"]}`,
		"liste vide":                             `{"components": []}`,
		"document null":                          `null`,
		"document sans composant":                `{"state": "present"}`,
		"état absent (F21)":                      `{"components": ["reboot"], "state": "absent"}`,
		"état inventé (F21)":                     `{"components": ["reboot"], "state": "nimporte"}`,
		"composant inconnu parmi d'autres (F23)": `{"components": ["reboot", "bsae"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			ran := fakeComponents(t, nil)
			var stdout, stderr bytes.Buffer
			code := Run([]string{"system", "apply"}, strings.NewReader(doc), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exitCode = %d, want 2", code)
			}
			if len(ran()) != 0 {
				t.Errorf("no component may run on an unreadable request, ran %v", ran())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "rien n'a été appliqué") {
				t.Errorf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}

	t.Run("argument inattendu", func(t *testing.T) {
		ran := fakeComponents(t, nil)
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"system", "apply", "--dry-run"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Errorf("exitCode = %d, want 2", code)
		}
		if len(ran()) != 0 {
			t.Errorf("ran %v", ran())
		}
	})

	t.Run("état present explicite : appliqué", func(t *testing.T) {
		ran := fakeComponents(t, nil)
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"system", "apply"}, strings.NewReader(`{"components": ["reboot"], "state": "present"}`), &stdout, &stderr); code != 0 {
			t.Fatalf("exitCode = %d (stderr=%s)", code, stderr.String())
		}
		if strings.Join(ran(), ",") != "reboot" {
			t.Errorf("ran %v, want reboot", ran())
		}
	})

	t.Run("stdin vide : composants par défaut", func(t *testing.T) {
		ran := fakeComponents(t, nil)
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"system", "apply"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("exitCode = %d (stderr=%s)", code, stderr.String())
		}
		if strings.Join(ran(), ",") != "base,prj,reboot" {
			t.Errorf("ran %v, want base,prj,reboot", ran())
		}
	})
}

// TestApplyKeepsFailedComponentReport guards F20: a failing component keeps what it reported.
func TestApplyKeepsFailedComponentReport(t *testing.T) {
	origSkip := skipRootCheckForTest
	skipRootCheckForTest = true
	defer func() { skipRootCheckForTest = origSkip }()

	run := func(t *testing.T, script string) ApplyResult {
		t.Helper()
		fakeComponents(t, map[string]string{"prj": script})
		var stdout, stderr bytes.Buffer
		code := Run([]string{"system", "apply"}, strings.NewReader(`{"components": ["prj"]}`), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exitCode = %d, want 1", code)
		}
		var res ApplyResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("apply output is not JSON: %v (%q)", err, stdout.String())
		}
		return res
	}

	t.Run("code non nul après son compte rendu : changements et échecs gardés, stderr cité", func(t *testing.T) {
		res := run(t, `echo '{"component": "prj", "changed": 2, "failures": ["étape 3"]}'; echo 'boom' >&2; exit 3`)
		if res.Changed != 2 {
			t.Errorf("changed = %d, want 2", res.Changed)
		}
		all := strings.Join(res.Failures, "\n")
		for _, want := range []string{"étape 3", "exit status 3", "stderr : boom"} {
			if !strings.Contains(all, want) {
				t.Errorf("failures %q lack %q", res.Failures, want)
			}
		}
	})

	t.Run("délai dépassé : nommé, sans attendre l'enfant qui garde la sortie", func(t *testing.T) {
		origTimeout, origDelay := componentTimeout, componentWaitDelay
		componentTimeout, componentWaitDelay = 300*time.Millisecond, 300*time.Millisecond
		defer func() { componentTimeout, componentWaitDelay = origTimeout, origDelay }()
		start := time.Now()
		res := run(t, `sleep 4; echo never`)
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("apply waited %s for a child holding stdout", elapsed)
		}
		if all := strings.Join(res.Failures, "\n"); !strings.Contains(all, "délai de 300ms dépassé") {
			t.Errorf("failures %q must name the timeout", res.Failures)
		}
	})

	t.Run("tué avant son compte rendu : la perte est dite, stderr cité", func(t *testing.T) {
		res := run(t, `echo 'étape 2 faite' >&2; kill -KILL $$`)
		all := strings.Join(res.Failures, "\n")
		for _, want := range []string{"signal: killed", "ses changements ne sont pas comptés", "stderr : étape 2 faite"} {
			if !strings.Contains(all, want) {
				t.Errorf("failures %q lack %q", res.Failures, want)
			}
		}
	})
}

// TestRecordWritesOnceKnownNames guards AD-2 (F18): known names only, each written once.
func TestRecordWritesOnceKnownNames(t *testing.T) {
	origSkip := skipRootCheckForTest
	skipRootCheckForTest = true
	defer func() { skipRootCheckForTest = origSkip }()

	record := func(doc string) (int, RecordResult, string) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"system", "record"}, strings.NewReader(doc), &stdout, &stderr)
		var res RecordResult
		_ = json.Unmarshal(stdout.Bytes(), &res)
		return code, res, stderr.String()
	}

	for name, doc := range map[string]string{
		"jalon inconnu":                    `{"milestones": [{"name": "n_importe_quoi", "reached_at": "2026-11-09T09:00:00Z"}]}`,
		"attestation inconnue":             `{"attestations": [{"type": "inconnue", "attested_at": "2026-11-09T09:00:00Z"}]}`,
		"jalon sans date":                  `{"milestones": [{"name": "activated_at"}]}`,
		"clé mal orthographiée":            `{"milestone": [{"name": "activated_at", "reached_at": "2026-11-09T09:00:00Z"}]}`,
		"attestation sans date":            `{"attestations": [{"type": "recovery_kit_verified"}]}`,
		"document vide":                    `{}`,
		"document null":                    `null`,
		"preuve incomplète":                `{"proofs": [{}]}`,
		"jalon en double dans le document": `{"milestones": [{"name": "activated_at", "reached_at": "2026-11-09T09:00:00Z"}, {"name": "activated_at", "reached_at": "2026-11-09T10:00:00Z"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MUXFLEET_STATUS_DIR", dir)
			code, _, stderr := record(doc)
			if code != 2 || !strings.Contains(stderr, "rien n'a été écrit") {
				t.Errorf("code = %d stderr = %q, want 2 and « rien n'a été écrit »", code, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "system_records.json")); !os.IsNotExist(err) {
				t.Errorf("a refused record must not create the state file")
			}
		})
	}

	t.Run("argument inattendu", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", dir)
		var stdout, stderr bytes.Buffer
		doc := `{"milestones": [{"name": "activated_at", "reached_at": "2026-11-09T09:00:00Z"}]}`
		if code := Run([]string{"system", "record", "--dry-run"}, strings.NewReader(doc), &stdout, &stderr); code != 2 {
			t.Errorf("exitCode = %d, want 2", code)
		}
		if _, err := os.Stat(filepath.Join(dir, "system_records.json")); !os.IsNotExist(err) {
			t.Errorf("a refused record must not create the state file")
		}
	})

	t.Run("sortie JSON avec schema_version (AD-19)", func(t *testing.T) {
		t.Setenv("MUXFLEET_STATUS_DIR", t.TempDir())
		_, res, _ := record(`{"proofs": [{"fact": "f", "source": "s", "observed_at": "2026-11-09T09:00:00Z"}]}`)
		if res.SchemaVersion != SchemaVersion || !res.Recorded {
			t.Errorf("result = %+v, want schema_version %s and recorded", res, SchemaVersion)
		}
	})

	t.Run("enregistrements simultanés : aucune écriture perdue (F22)", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("MUXFLEET_STATUS_DIR", dir)
		const n = 20
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				doc := fmt.Sprintf(`{"proofs": [{"fact": "f%d", "source": "s", "observed_at": "2026-11-09T09:00:00Z"}]}`, i)
				var stdout, stderr bytes.Buffer
				codes[i] = Run([]string{"system", "record"}, strings.NewReader(doc), &stdout, &stderr)
			}(i)
		}
		wg.Wait()
		for i, c := range codes {
			if c != 0 {
				t.Fatalf("record %d: code %d", i, c)
			}
		}
		recs, err := readRecords(filepath.Join(dir, "system_records.json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(recs.Proofs) != n {
			t.Errorf("%d proofs kept out of %d recorded at once", len(recs.Proofs), n)
		}
	})

	t.Run("deuxième enregistrement : la première date est gardée", func(t *testing.T) {
		t.Setenv("MUXFLEET_STATUS_DIR", t.TempDir())
		first := `{"milestones": [{"name": "activated_at", "reached_at": "2026-11-06T12:00:00Z"}], "attestations": [{"type": "recovery_kit_verified", "attested_at": "2026-11-06T11:00:00Z"}]}`
		again := `{"milestones": [{"name": "activated_at", "reached_at": "2026-11-09T09:00:00Z"}], "attestations": [{"type": "recovery_kit_verified", "attested_at": "2026-11-09T09:00:00Z"}]}`
		if code, res, stderr := record(first); code != 0 || len(res.AlreadyRecorded) != 0 {
			t.Fatalf("first record: code=%d res=%+v stderr=%s", code, res, stderr)
		}
		code, res, stderr := record(again)
		if code != 0 {
			t.Fatalf("a repeated record is not an error (vps may retry): code=%d stderr=%s", code, stderr)
		}
		if strings.Join(res.AlreadyRecorded, ",") != "activated_at,recovery_kit_verified" {
			t.Errorf("already_recorded = %v", res.AlreadyRecorded)
		}

		var stdout, serr bytes.Buffer
		if code := Run([]string{"system", "status", "--json"}, nil, &stdout, &serr); code != 0 {
			t.Fatalf("status code=%d stderr=%s", code, serr.String())
		}
		var st StatusResult
		if err := json.Unmarshal(stdout.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if len(st.Facts.Milestones) != 1 || len(st.Facts.Attestations) != 1 {
			t.Fatalf("milestones=%d attestations=%d, want 1 and 1", len(st.Facts.Milestones), len(st.Facts.Attestations))
		}
		if got := st.Facts.Milestones[0].ReachedAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-11-06T12:00:00Z" {
			t.Errorf("activated_at = %s, want the first date 2026-11-06T12:00:00Z", got)
		}
	})
}

func TestTailText(t *testing.T) {
	if got := tailText("a\n\nb\nc\nd\n", 3, 100); got != "b | c | d" {
		t.Errorf("got %q", got)
	}
	got := tailText("début "+strings.Repeat("é", 300)+" fin", 3, 11)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, " fin") || strings.Contains(got, "début") {
		t.Errorf("truncation must keep the end, got %q", got)
	}
	if !utf8.ValidString(got) || len(got) > len("…")+11 {
		t.Errorf("truncation must keep valid UTF-8 within the limit, got %q", got)
	}
}
