package components

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helperBlock returns a component script's helper functions: from record_failure up to its first step.
func helperBlock(t *testing.T, name string) string {
	t.Helper()
	script, err := GetComponentScript(name)
	if err != nil {
		t.Fatal(err)
	}
	s := string(script)
	start := strings.Index(s, "# record_failure adds")
	end := strings.Index(s, "\n# 1. ")
	if start < 0 || end < start {
		t.Fatalf("%s: helper block not found", name)
	}
	return s[start:end]
}

// TestComponentHelpersAreIdentical keeps the copies of the helpers in the three scripts in step.
func TestComponentHelpersAreIdentical(t *testing.T) {
	want := helperBlock(t, "base")
	for _, name := range []string{"prj", "reboot"} {
		if got := helperBlock(t, name); got != want {
			t.Errorf("%s/apply.sh helpers differ from base/apply.sh", name)
		}
	}
}

// runHelpers runs body after the helpers in bash, as the scripts do (set -euo pipefail), and
// returns its stdout lines and the failures recorded, decoded as the JSON the script prints.
func runHelpers(t *testing.T, body string) ([]string, []string) {
	t.Helper()
	prog := "set -euo pipefail\nCHANGED=0\nFAILURES=()\n" + helperBlock(t, "base") + "\n" + body + `
IFS=','
echo "FAILURES=[${FAILURES[*]:-}]"
`
	out, err := exec.Command("bash", "-c", prog).CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	var lines []string
	var failures []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(l, "FAILURES=") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "FAILURES=")), &failures); err != nil {
				t.Fatalf("failures are not valid JSON: %v (%s)", err, l)
			}
			continue
		}
		lines = append(lines, l)
	}
	return lines, failures
}

func rc(call string) string { return `set +e; ` + call + `; echo "rc=$?"; set -e` }

// TestWriteIfChanged guards F25: a write that fails is reported, never counted as a change.
func TestWriteIfChanged(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "conf")

	lines, failures := runHelpers(t, strings.Join([]string{
		rc(`write_if_changed "` + f + `" "v=1" 0644`), // new file
		rc(`write_if_changed "` + f + `" "v=1" 0644`), // same content and mode
		`chmod 0600 "` + f + `"`,
		rc(`write_if_changed "` + f + `" "v=1" 0644`), // mode drift only
		`printf 'v=2' > "` + f + `"`,
		rc(`write_if_changed "` + f + `" "v=1" 0644`), // content drift
		`mkdir "` + f + `.d"`,
		rc(`write_if_changed "` + f + `.d" "v=1" 0644`), // a directory stands in the way
	}, "\n"))
	if got := strings.Join(lines, " "); got != "rc=0 rc=1 rc=0 rc=0 rc=2" {
		t.Errorf("return codes = %s, want rc=0 rc=1 rc=0 rc=0 rc=2", got)
	}
	if data, _ := os.ReadFile(f); string(data) != "v=1" {
		t.Errorf("content = %q, want v=1", data)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0644 {
		t.Errorf("mode = %o, want 644", fi.Mode().Perm())
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "is a directory") {
		t.Errorf("failures = %q, want one about the directory", failures)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".muxfleet-tmp.*")); len(entries) != 0 {
		t.Errorf("temporary files left behind: %v", entries)
	}

	t.Run("dossier non inscriptible : échec signalé, fichier inchangé", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		ro := t.TempDir()
		target := filepath.Join(ro, "conf")
		if err := os.WriteFile(target, []byte("v=60"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ro, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(ro, 0755)

		lines, failures := runHelpers(t, rc(`write_if_changed "`+target+`" "v=10" 0644`)+"\n"+
			`if write_if_changed "`+target+`" "v=10" 0644; then CHANGED=$((CHANGED + 1)); fi; echo "changed=$CHANGED"`)
		if got := strings.Join(lines, " "); got != "rc=2 changed=0" {
			t.Errorf("got %s, want rc=2 changed=0", got)
		}
		if len(failures) != 2 {
			t.Errorf("failures = %q, want one per attempt", failures)
		}
		if data, _ := os.ReadFile(target); string(data) != "v=60" {
			t.Errorf("content = %q, must stay v=60", data)
		}
	})
}

// TestEnsureMode guards F26: a chmod that fails is reported, never counted as a change.
func TestEnsureMode(t *testing.T) {
	f := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(f, 0755); err != nil {
		t.Fatal(err)
	}
	body := rc(`ensure_mode "`+f+`" 0700`) + "\n" + rc(`ensure_mode "`+f+`" 0700`)
	if os.Geteuid() != 0 {
		body += "\n" + rc(`ensure_mode / 0700`) // not ours: chmod fails
	}
	lines, failures := runHelpers(t, body)
	want := "rc=0 rc=1"
	if os.Geteuid() != 0 {
		want += " rc=2"
	}
	if got := strings.Join(lines, " "); got != want {
		t.Errorf("return codes = %s, want %s", got, want)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0700 {
		t.Errorf("mode = %o, want 700", fi.Mode().Perm())
	}
	if os.Geteuid() != 0 && (len(failures) != 1 || !strings.Contains(failures[0], "/: chmod 0700 failed")) {
		t.Errorf("failures = %q", failures)
	}
}

// TestRecordFailureEscapes keeps the failures list valid JSON whatever the message holds.
func TestRecordFailureEscapes(t *testing.T) {
	_, failures := runHelpers(t, `record_failure 'quote " and backslash \ in a message'`)
	if len(failures) != 1 || failures[0] != `quote " and backslash \ in a message` {
		t.Errorf("failures = %q", failures)
	}
}
