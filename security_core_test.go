package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityCoreUpdatesReleaseGroup(t *testing.T) {
	for _, tc := range []struct {
		name, core, exclude, scope string
		skip                       bool
	}{{"core-group", "true", "", "all", false}, {"disabled", "false", "", "all", false}, {"excluded", "true", "core-recommended", "all", false}, {"contrib-scope", "true", "", "contrib", false}, {"patched", "true", "", "all", true}} {
		t.Run(tc.name, func(t *testing.T) {
			original := `{"require":{"drupal/core-recommended":"^11","drupal/core-composer-scaffold":"^11","drupal/core-project-message":"^11","drupal/core-recipe-unpack":"^11","drupal/gin":"^5.0"},"require-dev":{"drupal/core-dev":"^11"},"extra":{"patches":{"drupal/core":{"fix":"https://example.test/core.patch"}}}}`
			lock := `{"packages":[{"name":"drupal/core","version":"11.4.8"},{"name":"drupal/core-recommended","version":"11.4.8"},{"name":"drupal/core-composer-scaffold","version":"11.4.8"},{"name":"drupal/core-project-message","version":"11.4.8"},{"name":"drupal/core-recipe-unpack","version":"11.4.8"},{"name":"drupal/gin","version":"5.0.15"}],"packages-dev":[{"name":"drupal/core-dev","version":"11.4.8"}]}`
			modeFixture(t, original, lock)
			prevSelection, prevSkip := activeSelection, skipPatched
			activeSelection = selection{scope: tc.scope}
			skipPatched = tc.skip
			t.Cleanup(func() { activeSelection, skipPatched = prevSelection, prevSkip })
			audits := 0
			updates := 0
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "audit" {
					audits++
					if audits == 1 {
						return []byte(`{"advisories":{"drupal/core":[{"title":"core vulnerability"}],"drupal/gin":[{"title":"gin vulnerability"}]}}`), "", composerExit(1)
					}
					return []byte(`{"advisories":[]}`), "", nil
				}
				if args[0] != "update" {
					t.Fatalf("unexpected discovery command: %v", args)
				}
				updates++
				wantCore := tc.name == "core-group"
				selected := map[string]bool{}
				for _, arg := range args[1:] {
					if strings.HasPrefix(arg, "drupal/") {
						selected[arg] = true
					}
				}
				if !selected["drupal/gin"] {
					t.Fatal("missing affected contrib")
				}
				for _, name := range coreReleasePackages {
					if selected[name] != wantCore {
						t.Errorf("core package %s selected=%t, want %t", name, selected[name], wantCore)
					}
				}
				for _, arg := range args {
					if strings.Contains(arg, "no-blocking") || strings.Contains(arg, "ignore-platform") || strings.Contains(arg, "no-security") {
						t.Fatal("weakened Composer checks")
					}
				}
				got, _ := os.ReadFile(ComposerJsonFile)
				if string(got) != original {
					t.Fatal("security changed constraints")
				}
				return nil, "", nil
			}
			if e := runMode("security", tc.core, tc.exclude, "report.md"); e != nil {
				t.Fatal(e)
			}
			if audits != 2 || updates != 1 {
				t.Fatalf("audit=%d updates=%d", audits, updates)
			}
		})
	}
}

// Audit responses are controlled, but dependency resolution is performed by real
// Composer. core-recommended pins core exactly, reproducing the reported failure.
func TestComposerSecurityCoreRecommendedIntegration(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	modeFixture(t, `{"name":"test/core-security","require":{"drupal/core-recommended":"^11","drupal/core-composer-scaffold":"^11","drupal/core-project-message":"^11","drupal/gin":"^5.0","drupal/addtoany":"^2"},"require-dev":{"drupal/core-dev":"^11"}}`, `{"packages":[],"packages-dev":[]}`)
	m, e := readManifest()
	if e != nil {
		t.Fatal(e)
	}
	var packages []map[string]interface{}
	for _, release := range []string{"11.4.8", "11.4.11"} {
		for _, name := range []string{"drupal/core", "drupal/core-recommended", "drupal/core-composer-scaffold", "drupal/core-project-message", "drupal/core-dev"} {
			p := map[string]interface{}{"name": name, "version": release, "type": "metapackage"}
			if name == "drupal/core-recommended" || name == "drupal/core-dev" {
				p["require"] = map[string]string{"drupal/core": release}
			}
			packages = append(packages, p)
		}
	}
	for _, release := range []string{"5.0.15", "5.0.16"} {
		packages = append(packages, map[string]interface{}{"name": "drupal/gin", "version": release, "type": "metapackage", "require": map[string]string{"drupal/core": "^11.2"}})
	}
	packages = append(packages, map[string]interface{}{"name": "drupal/addtoany", "version": "2.0.7", "type": "metapackage", "require": map[string]string{"drupal/core": "^10.1 || ^11"}})
	m["repositories"], _ = json.Marshal([]interface{}{map[string]interface{}{"type": "package", "package": packages}, map[string]interface{}{"packagist.org": false}})
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
	args := []string{"update", "--with", "drupal/core-recommended:11.4.8", "--with", "drupal/core-composer-scaffold:11.4.8", "--with", "drupal/core-project-message:11.4.8", "--with", "drupal/core-dev:11.4.8", "--with", "drupal/gin:5.0.15", "--no-interaction", "--no-progress"}
	_, stderr, e := realComposer(args...)
	if e != nil {
		t.Fatalf("seed: %v %s", e, stderr)
	}
	before, e := os.ReadFile(ComposerJsonFile)
	if e != nil {
		t.Fatal(e)
	}
	audits := 0
	runComposer = func(args ...string) ([]byte, string, error) {
		if args[0] == "audit" {
			audits++
			if audits == 1 {
				return []byte(`{"advisories":{"drupal/core":[{}],"drupal/gin":[{}]}}`), "", composerExit(1)
			}
			installed, e := lockedPackages()
			if e != nil {
				t.Fatal(e)
			}
			if installed["drupal/core"] == "11.4.8" || installed["drupal/gin"] == "5.0.15" {
				return []byte(`{"advisories":{"drupal/core":[{}]}}`), "", composerExit(1)
			}
			return []byte(`{"advisories":[]}`), "", nil
		}
		return realComposer(args...)
	}
	if e = runMode("security", "true", "", "report.md"); e != nil {
		t.Fatal(e)
	}
	installed, e := lockedPackages()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"drupal/core", "drupal/core-recommended", "drupal/core-composer-scaffold", "drupal/core-project-message", "drupal/core-dev"} {
		if installed[name] != "11.4.11" {
			t.Errorf("%s stayed locked: %s", name, installed[name])
		}
	}
	if installed["drupal/gin"] != "5.0.16" || installed["drupal/addtoany"] != "2.0.7" {
		t.Fatal(installed)
	}
	after, e := os.ReadFile(ComposerJsonFile)
	if e != nil {
		t.Fatal(e)
	}
	if string(before) != string(after) {
		t.Fatal("constraints changed")
	}
}
