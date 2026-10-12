package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestComposerAnnotationDeduplication(t *testing.T) {
	message := "Your requirements could not be resolved to an installable set of packages.\n\n  Problem 1\n    - google/recaptcha 1.5.2 requires php >=8.4 -> your php version (8.3.35) does not satisfy that requirement.\n"
	annotation := "::error ::" + strings.ReplaceAll(message, "\n", "%0A") + "\n"
	cleaned := cleanComposerOutput(annotation, "\x1b[31mLoading composer repositories with package information\x1b[0m\nUpdating dependencies\n"+message)
	if strings.Count(cleaned, "Your requirements") != 1 || strings.Contains(cleaned, "%0A") || strings.Contains(cleaned, "::error") || strings.Contains(cleaned, "\x1b") {
		t.Fatal(cleaned)
	}
	report := failureMarkdown("update failed: exit status 2\n" + cleaned)
	if !strings.Contains(report, "```text\n") || !strings.Contains(report, "\n  Problem 1\n") {
		t.Fatal(report)
	}
	if got := cleanComposerOutput(annotation, ""); !strings.Contains(got, "\n  Problem 1\n") {
		t.Fatal(got)
	}
}
func TestPatchSkipRetryPreservesRequirements(t *testing.T) {
	modeFixture(t, `{"require":{"drupal/token":"^1","drupal/redirect":"^1"},"extra":{"patches":{"drupal/token":{"fix":"https://example.test/fix.patch"}}}}`, `{"packages":[{"name":"drupal/token","version":"1.0.0"},{"name":"drupal/redirect","version":"1.0.0"}]}`)
	old := patchFailurePolicy
	patchFailurePolicy = "skip"
	t.Cleanup(func() { patchFailurePolicy = old })
	calls := 0
	runComposer = func(args ...string) ([]byte, string, error) {
		if args[0] == "show" {
			return []byte(`{"versions":["1.1.0"]}`), "", nil
		}
		calls++
		if calls == 1 {
			return nil, "Could not apply patch https://example.test/fix.patch", composerExit(1)
		}
		if strings.Contains(strings.Join(args, " "), "drupal/token") {
			t.Fatal("retried failed target")
		}
		m, e := readManifest()
		if e != nil {
			t.Fatal(e)
		}
		if requirements(m)["drupal/token"] != "^1" {
			t.Fatal("skipped constraint was not restored")
		}
		return nil, "", os.WriteFile(ComposerLockFile, []byte(`{"packages":[{"name":"drupal/token","version":"1.0.0"},{"name":"drupal/redirect","version":"1.1.0"}]}`), 0600)
	}
	if e := runMode("minor", "true", "", "report.md"); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	m, _ := readManifest()
	if !strings.Contains(string(m["extra"]), "fix.patch") {
		t.Fatal("patch removed")
	}
	b, _ := os.ReadFile("report.md")
	if !strings.Contains(string(b), "after a patch failure") {
		t.Fatal("skip not reported")
	}
}
func TestSkipPatchedCoreFromExternalDefinitions(t *testing.T) {
	modeFixture(t, `{"require":{"drupal/core-recommended":"^11","drupal/token":"^1"},"extra":{"patches-file":"patches.json"}}`, `{"packages":[{"name":"drupal/core-recommended","version":"11.0.0"},{"name":"drupal/token","version":"1.0.0"}]}`)
	if e := os.WriteFile("patches.json", []byte(`{"patches":{"drupal/core":[{"url":"https://example.test/core.patch"}]}}`), 0600); e != nil {
		t.Fatal(e)
	}
	old := skipPatched
	skipPatched = true
	t.Cleanup(func() { skipPatched = old })
	runComposer = func(args ...string) ([]byte, string, error) {
		if strings.Contains(strings.Join(args, " "), "drupal/core") {
			t.Fatal("selected patched core")
		}
		if args[0] == "show" {
			return []byte(`{"versions":["1.1.0"]}`), "", nil
		}
		return nil, "", os.WriteFile(ComposerLockFile, []byte(`{"packages":[{"name":"drupal/core-recommended","version":"11.0.0"},{"name":"drupal/token","version":"1.1.0"}]}`), 0600)
	}
	if e := runMode("minor", "true", "", "report.md"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile("report.md")
	if !strings.Contains(string(b), "configured patches retained") {
		t.Fatal("skip not reported")
	}
}
func TestPatchFailureIdentificationRefusesAmbiguity(t *testing.T) {
	patches := map[string]json.RawMessage{"drupal/token": json.RawMessage(`{"fix":"https://example.test/one.patch"}`), "drupal/redirect": json.RawMessage(`{"fix":"https://example.test/two.patch"}`)}
	if got := failedPatchTarget("Could not apply patch https://example.test/one.patch https://example.test/two.patch", patches, []string{"drupal/token", "drupal/redirect"}); got != "" {
		t.Fatal(got)
	}
}

func TestPatchSkipAllTargetsRestoresSnapshot(t *testing.T) {
	original := `{"require":{"drupal/token":"^1"},"extra":{"patches":{"drupal/token":{"fix":"https://example.test/fix.patch"}}}}`
	lock := `{"packages":[{"name":"drupal/token","version":"1.0.0"}]}`
	modeFixture(t, original, lock)
	previous := patchFailurePolicy
	patchFailurePolicy = "skip"
	t.Cleanup(func() { patchFailurePolicy = previous })
	attempts := 0
	runComposer = func(args ...string) ([]byte, string, error) {
		if args[0] == "show" {
			return []byte(`{"versions":["1.1.0"]}`), "", nil
		}
		attempts++
		_ = os.WriteFile(ComposerLockFile, []byte(`{"changed":true}`), 0600)
		return nil, "Could not apply patch https://example.test/fix.patch", composerExit(1)
	}
	if e := runMode("minor", "true", "", "report.md"); e != nil {
		t.Fatal(e)
	}
	if attempts != 1 {
		t.Fatal("unbounded retry")
	}
	for path, want := range map[string]string{ComposerJsonFile: original, ComposerLockFile: lock} {
		b, _ := os.ReadFile(path)
		if string(b) != want {
			t.Fatalf("%s not restored", path)
		}
	}
	b, _ := os.ReadFile("report.md")
	if !strings.Contains(string(b), "All selected upgrades were skipped") {
		t.Fatal("skips reported as upgrades")
	}
}

func TestPatchFailurePolicyDoesNotSkipPlatformErrors(t *testing.T) {
	modeFixture(t, `{"require":{"drupal/token":"^1"},"extra":{"patches":{"drupal/token":{"fix":"https://example.test/fix.patch"}}}}`, `{"packages":[{"name":"drupal/token","version":"1.0.0"}]}`)
	previous := patchFailurePolicy
	patchFailurePolicy = "skip"
	t.Cleanup(func() { patchFailurePolicy = previous })
	runComposer = func(args ...string) ([]byte, string, error) {
		if args[0] == "show" {
			return []byte(`{"versions":["1.1.0"]}`), "", nil
		}
		return nil, "google/recaptcha requires php >=8.4; current PHP is 8.3.35", composerExit(2)
	}
	if e := runMode("minor", "true", "", "report.md"); e == nil {
		t.Fatal("platform failure suppressed")
	}
	b, _ := os.ReadFile("report.md")
	if !strings.Contains(string(b), "```text\n") || !strings.Contains(string(b), "php >=8.4") {
		t.Fatal("lost readable platform error")
	}
}
