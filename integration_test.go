package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real solver without network access or Drupal downloads.
func TestComposerModesIntegration(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	for _, tc := range []struct{ mode, want, constraint string }{{"minor", "1.5.0", "1.0.0"}, {"major", "2.0.0", "1.0.0"}, {"security", "1.0.0", "1.0.0"}, {"nuclear", "2.0.0", "1.0.0"}, {"minor", "1.5.0", "^1.0"}, {"major", "2.0.0", "^1.0"}, {"major", "2.0.0", "~1.0"}, {"major", "2.0.0", "1.*"}, {"all", "2.0.0", "^1.0"}} {
		t.Run(tc.mode, func(t *testing.T) {
			modeFixture(t, `{"name":"test/fixture","require":{"drupal/demo":"1.0.0"},"repositories":[{"type":"package","package":[{"name":"drupal/demo","version":"1.0.0","type":"metapackage"},{"name":"drupal/demo","version":"1.5.0","type":"metapackage"},{"name":"drupal/demo","version":"2.0.0","type":"metapackage"}]},{"packagist.org":false}]}`, `{"packages":[]}`)
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
			m, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			if e = setConstraint(m, "drupal/demo", tc.constraint); e != nil {
				t.Fatal(e)
			}
			if e = writeManifest(m); e != nil {
				t.Fatal(e)
			}
			stdout, stderr, e := runComposer("update", "--with", "drupal/demo:1.0.0", "--no-interaction", "--no-progress")
			if e != nil {
				t.Fatalf("fixture: %v\n%s%s", e, stdout, stderr)
			}
			if tc.mode == "nuclear" {
				m, e := readManifest()
				if e != nil {
					t.Fatal(e)
				}
				if e = setConstraint(m, "drupal/demo", "9.0.0"); e != nil {
					t.Fatal(e)
				}
				if e = writeManifest(m); e != nil {
					t.Fatal(e)
				}
			}
			if e = updateProjects(tc.mode, "true", "", "report.md"); e != nil {
				t.Fatal(e)
			}
			locked, e := lockedPackages()
			if e != nil {
				t.Fatal(e)
			}
			if locked["drupal/demo"] != tc.want {
				t.Fatalf("got %s, want %s", locked["drupal/demo"], tc.want)
			}
			b, e := os.ReadFile(ComposerJsonFile)
			if e != nil {
				t.Fatal(e)
			}
			m, e = readManifest()
			if e != nil {
				t.Fatal(e)
			}
			if tc.mode != "security" && tc.mode != "nuclear" {
				want, e := upgradeConstraint(tc.constraint, tc.want)
				if e != nil {
					t.Fatal(e)
				}
				if got := requirements(m)["drupal/demo"]; got != want {
					t.Fatalf("constraint: %s, want %s", got, want)
				}
			}
			if !json.Valid(b) {
				t.Fatal("invalid manifest")
			}
		})
	}
}

func TestComposerSelectionIntegration(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	for _, tc := range []struct{ scope, packages string }{{"core", ""}, {"contrib", ""}, {"contrib", "demo"}} {
		t.Run(tc.scope+tc.packages, func(t *testing.T) {
			names := []string{"drupal/core-recommended", "drupal/demo", "drupal/other"}
			var packages []map[string]string
			roots := map[string]string{}
			for _, name := range names {
				roots[name] = "1.0.0"
				for _, release := range []string{"1.0.0", "1.5.0"} {
					packages = append(packages, map[string]string{"name": name, "version": release, "type": "metapackage"})
				}
			}
			project := map[string]interface{}{"name": "test/selection", "require": roots, "repositories": []interface{}{map[string]interface{}{"type": "package", "package": packages}, map[string]interface{}{"packagist.org": false}}}
			b, e := json.Marshal(project)
			if e != nil {
				t.Fatal(e)
			}
			modeFixture(t, string(b), `{"packages":[]}`)
			dir, e := os.Getwd()
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("COMPOSER_HOME", filepath.Join(dir, "composer-home"))
			t.Setenv("COMPOSER_CACHE_DIR", filepath.Join(dir, "composer-cache"))
			t.Setenv("COMPOSER_DISABLE_NETWORK", "1")
			t.Setenv("COMPOSER", ComposerJsonFile)
			for _, key := range []string{"INPUT_UPDATE_SCOPE", "INPUT_UPDATE_PACKAGES", "INPUT_COMPOSER_COMMAND", "INPUT_UPDATE_CORE", "INPUT_UPDATE_EXCLUDE"} {
				t.Setenv(key, "")
			}
			if e = os.Remove(ComposerLockFile); e != nil {
				t.Fatal(e)
			}
			_, stderr, e := runComposer("update", "--no-interaction", "--no-progress")
			if e != nil {
				t.Fatalf("fixture: %v %s", e, stderr)
			}
			if e = newApp().Run([]string{"drupal-update", "--type", "minor", "--scope", tc.scope, "--packages", tc.packages, "--composer-command", "composer", "--core", "true"}); e != nil {
				t.Fatal(e)
			}
			locked, e := lockedPackages()
			if e != nil {
				t.Fatal(e)
			}
			selected, e := parseSelection(tc.scope, tc.packages)
			if e != nil {
				t.Fatal(e)
			}
			for _, name := range names {
				want := "1.0.0"
				if selected.includes(name) {
					want = "1.5.0"
				}
				if locked[name] != want {
					t.Fatalf("%s: got %s, want %s", name, locked[name], want)
				}
			}
		})
	}
}

// Inspect actual fixture locks with Composer, without contacting repositories
// or executing the plugins and scripts contained in the supplied projects.
func TestComposerRealFixtureBaselines(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	for _, project := range projectFixtures(t) {
		t.Run(project, func(t *testing.T) {
			_, locked, _ := copyProjectFixture(t, project)
			dir, e := os.Getwd()
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("COMPOSER_HOME", filepath.Join(dir, "composer-home"))
			t.Setenv("COMPOSER_CACHE_DIR", filepath.Join(dir, "composer-cache"))
			t.Setenv("COMPOSER_DISABLE_NETWORK", "1")
			t.Setenv("COMPOSER", ComposerJsonFile)
			b, stderr, e := runComposer("show", "--locked", "--format=json", "--no-plugins", "--no-scripts")
			if e != nil {
				t.Fatalf("inspect lock: %v %s", e, stderr)
			}
			var discovered Outdated
			if e = json.Unmarshal(b, &discovered); e != nil {
				t.Fatal(e)
			}
			if len(discovered.Packages) != len(locked) {
				t.Fatalf("Composer discovered %d packages; updater read %d", len(discovered.Packages), len(locked))
			}
			for _, pkg := range discovered.Packages {
				// Composer show decorates development versions with their source reference.
				reported := strings.Fields(pkg.Version)
				if len(reported) == 0 || reported[0] != locked[pkg.Name] {
					t.Errorf("Composer and updater disagree on %s", pkg.Name)
				}
			}
			_, stderr, e = runComposer("validate", "--no-check-publish", "--no-plugins", "--no-scripts")
			if project == "project_x" {
				if e == nil || !strings.Contains(stderr, "lock file is not up to date") {
					t.Fatalf("stale lock baseline changed: %v %s", e, stderr)
				}
			} else if e != nil {
				t.Fatalf("fixture does not validate: %v %s", e, stderr)
			}
		})
	}
}

func TestComposerRetainedCompoundConstraint(t *testing.T) {
	if _, e := exec.LookPath("composer"); e != nil {
		t.Skip("Composer unavailable")
	}
	for _, tc := range []struct {
		mode string
		fail bool
	}{{"minor", false}, {"major", true}} {
		t.Run(tc.mode, func(t *testing.T) {
			source, _, _ := copyProjectFixture(t, "project_z")
			constraint := requirements(source)["drupal/commerce_shipping"]
			if !strings.Contains(constraint, "||") {
				t.Fatal("fixture no longer exercises compound constraints")
			}
			project := map[string]interface{}{"name": "test/compound", "require": map[string]string{"drupal/commerce_shipping": constraint}, "repositories": []interface{}{map[string]interface{}{"type": "package", "package": []map[string]string{{"name": "drupal/commerce_shipping", "version": "3.0.3", "type": "metapackage"}, {"name": "drupal/commerce_shipping", "version": "3.1.0", "type": "metapackage"}, {"name": "drupal/commerce_shipping", "version": "4.0.0", "type": "metapackage"}}}, map[string]interface{}{"packagist.org": false}}}
			b, e := json.Marshal(project)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(ComposerJsonFile, b, 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(ComposerLockFile); e != nil {
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
			_, stderr, e := runComposer("update", "--with", "drupal/commerce_shipping:3.0.3", "--no-interaction", "--no-progress")
			if e != nil {
				t.Fatalf("fixture: %v %s", e, stderr)
			}
			before, e := os.ReadFile(ComposerLockFile)
			if e != nil {
				t.Fatal(e)
			}
			e = runMode(tc.mode, "true", "", "")
			if (e != nil) != tc.fail {
				t.Fatalf("unexpected result: %v", e)
			}
			after, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			if requirements(after)["drupal/commerce_shipping"] != constraint {
				t.Fatal("compound constraint lost")
			}
			if tc.fail {
				afterLock, _ := os.ReadFile(ComposerLockFile)
				if string(afterLock) != string(before) {
					t.Fatal("failed solver attempt changed the lock")
				}
			} else {
				installed, e := lockedPackages()
				if e != nil {
					t.Fatal(e)
				}
				if installed["drupal/commerce_shipping"] != "3.1.0" {
					t.Fatal(installed)
				}
			}
		})
	}
}
