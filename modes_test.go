package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type composerExit int

func (e composerExit) Error() string { return "composer failed" }
func (e composerExit) ExitCode() int { return int(e) }
func modeFixture(t *testing.T, manifestText, lockText string) {
	t.Helper()
	old, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chdir(t.TempDir()); e != nil {
		t.Fatal(e)
	}
	original := runComposer
	t.Cleanup(func() { runComposer = original; _ = os.Chdir(old) })
	t.Setenv("GITHUB_ACTIONS", "")
	for p, b := range map[string]string{ComposerJsonFile: manifestText, ComposerLockFile: lockText} {
		if e = os.WriteFile(p, []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestVersionSelection(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{{"minor", "1.9.0"}, {"major", "3.0.0"}} {
		got, e := targetVersion("v1.2.3", []string{"1.2.4", "1.9.0", "2.0.0", "3.0.0", "4.0.0-beta1", "dev-main"}, tc.mode)
		if e != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.mode, got, e)
		}
	}
	if _, e := targetVersion("dev-main", nil, "minor"); e == nil {
		t.Fatal("accepted dev version")
	}
}
func TestCoordinatedCoreMajor(t *testing.T) {
	modeFixture(t, `{"require":{"drupal/core-recommended":"^10"},"require-dev":{"drupal/core-dev":"^10"},"extra":{"keep":true}}`, `{"packages":[{"name":"drupal/core-recommended","version":"10.4.0"}],"packages-dev":[{"name":"drupal/core-dev","version":"10.4.0"}]}`)
	runComposer = func(args ...string) ([]byte, string, error) {
		if args[0] == "show" {
			return []byte(`{"versions":["11.2.0","10.5.0"]}`), "", nil
		}
		m, e := readManifest()
		if e != nil {
			t.Fatal(e)
		}
		var dev map[string]string
		_ = json.Unmarshal(m["require-dev"], &dev)
		if dev["drupal/core-dev"] != "^11.2.0" {
			t.Fatal("development core constraint not coordinated")
		}
		if e = os.WriteFile(ComposerLockFile, []byte(`{"packages":[{"name":"drupal/core-recommended","version":"11.2.0"}],"packages-dev":[{"name":"drupal/core-dev","version":"11.2.0"}]}`), 0600); e != nil {
			t.Fatal(e)
		}
		return nil, "", nil
	}
	if e := runMode("major", "true", "", ""); e != nil {
		t.Fatal(e)
	}
}
func TestSecurityVerificationRollback(t *testing.T) {
	for _, fixed := range []bool{true, false} {
		t.Run(map[bool]string{true: "fixed", false: "unresolved"}[fixed], func(t *testing.T) {
			original := `{"require":{"drupal/token":"^1"}}`
			modeFixture(t, original, `{"packages":[{"name":"drupal/token","version":"1.0.0"},{"name":"drupal/other","version":"2.0.0"}]}`)
			audits := 0
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "audit" {
					audits++
					if fixed && audits == 2 {
						return []byte(`{"advisories":[]}`), "", nil
					}
					return []byte(`{"advisories":{"drupal/token":[{"title":"vulnerable"}]}}`), "", composerExit(1)
				}
				if strings.Contains(strings.Join(args, " "), "drupal/other") {
					t.Fatal("selected unaffected package")
				}
				_ = os.WriteFile(ComposerJsonFile, []byte(`{"changed":true}`), 0600)
				return nil, "", nil
			}
			e := runMode("security", "true", "", "")
			if (e == nil) != fixed {
				t.Fatalf("unexpected result: %v", e)
			}
			if !fixed {
				b, _ := os.ReadFile(ComposerJsonFile)
				if string(b) != original {
					t.Fatal("manifest not restored")
				}
			}
		})
	}
}
func TestNuclearRecovery(t *testing.T) {
	modeFixture(t, `{"require":{"drupal/token":"1.0.0"},"extra":{"patches":{"drupal/token":{"old fix":"https://example.test/fix.patch","keep":"https://example.test/keep.patch"}}}}`, `{"packages":[]}`)
	attempts := 0
	runComposer = func(args ...string) ([]byte, string, error) {
		attempts++
		switch attempts {
		case 1:
			return nil, "dependency conflict", composerExit(2)
		case 2:
			return nil, "Could not apply patch https://example.test/fix.patch", composerExit(1)
		}
		m, _ := readManifest()
		if requirements(m)["drupal/token"] != "*" {
			t.Fatal("constraint not relaxed")
		}
		if strings.Contains(string(m["extra"]), "fix.patch") {
			t.Fatal("failed patch retained")
		}
		if !strings.Contains(string(m["extra"]), "keep.patch") {
			t.Fatal("unrelated patch removed")
		}
		return nil, "", os.WriteFile(ComposerLockFile, []byte(`{"packages":[]}`), 0600)
	}
	if e := runMode("nuclear", "true", "", "recovery.md"); e != nil {
		t.Fatal(e)
	}
	if attempts != 3 {
		t.Fatal(attempts)
	}
}
func TestNuclearStopsAndRestores(t *testing.T) {
	original := `{"require":{"drupal/token":"^1"}}`
	lock := `{"packages":[]}`
	modeFixture(t, original, lock)
	runComposer = func(args ...string) ([]byte, string, error) { return nil, "network failure", errors.New("offline") }
	if e := runMode("nuclear", "true", "", ""); e == nil {
		t.Fatal("accepted failure")
	}
	for p, want := range map[string]string{ComposerJsonFile: original, ComposerLockFile: lock} {
		b, _ := os.ReadFile(p)
		if string(b) != want {
			t.Fatalf("%s not restored", p)
		}
	}
}
func TestAuditRejectsUnreachable(t *testing.T) {
	original := runComposer
	t.Cleanup(func() { runComposer = original })
	runComposer = func(args ...string) ([]byte, string, error) {
		return []byte(`{"advisories":[],"unreachable-repositories":["https://example.test"]}`), "", nil
	}
	if _, e := auditPackages(); e == nil {
		t.Fatal("accepted incomplete audit")
	}
}

func TestAmbiguousPatchFailure(t *testing.T) {
	var m manifest
	if e := json.Unmarshal([]byte(`{"extra":{"patches":{"drupal/token":{"first":"https://example.test/first.patch","second":"https://example.test/second.patch"}}}}`), &m); e != nil {
		t.Fatal(e)
	}
	before := string(m["extra"])
	if _, e := removeFailedPatch(m, "Applied https://example.test/first.patch; Could not apply patch https://example.test/second.patch"); e == nil {
		t.Fatal("removed an ambiguous patch")
	}
	if string(m["extra"]) != before {
		t.Fatal("changed patch definitions")
	}
}
