package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityPatchSelection(t *testing.T) {
	got, e := targetVersion("11.4.7", []string{"11.4.8", "11.4.10", "11.4.11-rc1", "11.5.0", "12.0.0"}, "patch")
	if e != nil || got != "11.4.10" {
		t.Fatalf("%s %v", got, e)
	}
	if got, e = targetVersion("11.4.7", []string{"11.5.0", "12.0.0"}, "patch"); e != nil || got != "" {
		t.Fatalf("crossed minor: %s %v", got, e)
	}
}

func TestComposerSecurityPinnedRequirements(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	for _, tc := range []struct {
		name           string
		enabled, fixed bool
	}{{"default-preserves-pins", false, true}, {"patch-pins", true, true}, {"no-fix-in-current-minor", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			modeFixture(t, `{"name":"test/pinned-security","require":{"drupal/core-recommended":"11.4.7","drupal/core-composer-scaffold":"11.4.7","drupal/core-project-message":"11.4.7","drupal/gin":"5.0.15","drupal/unrelated":"1.0.0"},"require-dev":{"drupal/core-dev":"11.4.7"}}`, `{"packages":[],"packages-dev":[]}`)
			previous := securityUpdatePinned
			securityUpdatePinned = tc.enabled
			t.Cleanup(func() { securityUpdatePinned = previous })
			m, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			var packages []map[string]any
			for _, release := range []string{"11.4.7", "11.4.10", "11.5.0", "12.0.0"} {
				for _, name := range []string{"drupal/core", "drupal/core-recommended", "drupal/core-composer-scaffold", "drupal/core-project-message", "drupal/core-dev"} {
					p := map[string]any{"name": name, "version": release, "type": "metapackage"}
					if name == "drupal/core-recommended" || name == "drupal/core-dev" {
						p["require"] = map[string]string{"drupal/core": release}
					}
					packages = append(packages, p)
				}
			}
			for _, release := range []string{"5.0.15", "5.0.16", "5.1.0", "6.0.0"} {
				packages = append(packages, map[string]any{"name": "drupal/gin", "version": release, "type": "metapackage", "require": map[string]string{"drupal/core": "^11"}})
			}
			for _, release := range []string{"1.0.0", "1.1.0"} {
				packages = append(packages, map[string]any{"name": "drupal/unrelated", "version": release, "type": "metapackage"})
			}
			m["repositories"], _ = json.Marshal([]any{map[string]any{"type": "package", "package": packages}, map[string]any{"packagist.org": false}})
			if e = writeManifest(m); e != nil {
				t.Fatal(e)
			}
			dir, e := os.Getwd()
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("COMPOSER_HOME", filepath.Join(dir, "composer-home"))
			t.Setenv("COMPOSER_CACHE_DIR", filepath.Join(dir, "composer-cache"))
			t.Setenv("COMPOSER_DISABLE_NETWORK", "1")
			t.Setenv("COMPOSER", ComposerJsonFile)
			if e = os.Remove(ComposerLockFile); e != nil {
				t.Fatal(e)
			}
			realComposer := runComposer
			_, stderr, e := realComposer("update", "--no-interaction", "--no-progress")
			if e != nil {
				t.Fatalf("seed: %v %s", e, stderr)
			}
			before, _ := os.ReadFile(ComposerJsonFile)
			lockBefore, _ := os.ReadFile(ComposerLockFile)
			audits := 0
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "audit" {
					audits++
					installed, e := lockedPackages()
					if e != nil {
						t.Fatal(e)
					}
					if tc.fixed && installed["drupal/core"] == "11.4.10" && installed["drupal/gin"] == "5.0.16" {
						return []byte(`{"advisories":[]}`), "", nil
					}
					return []byte(`{"advisories":{"drupal/core":[{}],"drupal/gin":[{}]}}`), "", composerExit(1)
				}
				return realComposer(args...)
			}
			e = runMode("security", "true", "", "report.md")
			if (e == nil) != (tc.enabled && tc.fixed) {
				t.Fatalf("unexpected result: %v", e)
			}
			if e != nil {
				after, _ := os.ReadFile(ComposerJsonFile)
				lockAfter, _ := os.ReadFile(ComposerLockFile)
				if string(after) != string(before) || string(lockAfter) != string(lockBefore) {
					t.Fatal("failed security fix did not restore original manifests/lock")
				}
				return
			}
			after, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			requirementsAfter := requirements(after)
			for _, name := range []string{"drupal/core-recommended", "drupal/core-composer-scaffold", "drupal/core-project-message", "drupal/core-dev"} {
				if requirementsAfter[name] != "11.4.10" {
					t.Errorf("%s did not remain exactly pinned: %s", name, requirementsAfter[name])
				}
			}
			if requirementsAfter["drupal/gin"] != "5.0.16" || requirementsAfter["drupal/unrelated"] != "1.0.0" {
				t.Fatal(requirementsAfter)
			}
			var dev map[string]string
			_ = json.Unmarshal(after["require-dev"], &dev)
			if dev["drupal/core-dev"] != "11.4.10" {
				t.Fatal("core-dev moved requirement section")
			}
			_, stderr, e = realComposer("validate", "--no-check-publish")
			if e != nil {
				t.Fatalf("rewritten exact pins do not match lock hash: %v %s", e, stderr)
			}
			report, _ := os.ReadFile("report.md")
			if !strings.Contains(string(report), "Advancing exact pin") {
				t.Fatal("pin changes not reported")
			}
			if audits != 2 {
				t.Fatal("missing post-update security verification")
			}
		})
	}
}
