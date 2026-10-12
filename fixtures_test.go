package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Copy full source manifests and locks. Composer responses in this suite are
// controlled: these projects may require private services, old PHP or plugins.
func projectFixtures(t *testing.T) []string {
	t.Helper()
	entries, e := os.ReadDir("fixtures")
	if e != nil {
		t.Fatal(e)
	}
	var projects []string
	for _, entry := range entries {
		if entry.IsDir() {
			projects = append(projects, entry.Name())
		}
	}
	if len(projects) < 3 {
		t.Fatal("expected the three supplied project fixtures")
	}
	return projects
}
func copyProjectFixture(t *testing.T, project string) (manifest, map[string]string, []byte) {
	t.Helper()
	source, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	source = filepath.Join(source, "fixtures", project)
	composerJSON, e := os.ReadFile(filepath.Join(source, ComposerJsonFile))
	if e != nil {
		t.Fatal(e)
	}
	composerLock, e := os.ReadFile(filepath.Join(source, ComposerLockFile))
	if e != nil {
		t.Fatal(e)
	}
	modeFixture(t, string(composerJSON), string(composerLock))
	// This cleanup also detects accidental writes to source fixtures.
	t.Cleanup(func() {
		for path, want := range map[string][]byte{ComposerJsonFile: composerJSON, ComposerLockFile: composerLock} {
			got, e := os.ReadFile(filepath.Join(source, path))
			if e != nil || string(got) != string(want) {
				t.Errorf("source fixture changed: %s/%s", project, path)
			}
		}
	})
	m, e := readManifest()
	if e != nil {
		t.Fatal(e)
	}
	locked, e := lockedPackages()
	if e != nil {
		t.Fatal(e)
	}
	return m, locked, composerLock
}
func writeFixtureResolution(t *testing.T, original []byte, targets map[string]string) {
	t.Helper()
	var lock map[string]json.RawMessage
	if e := json.Unmarshal(original, &lock); e != nil {
		t.Fatal(e)
	}
	for _, section := range []string{"packages", "packages-dev"} {
		var packages []map[string]json.RawMessage
		if e := json.Unmarshal(lock[section], &packages); e != nil {
			t.Fatal(e)
		}
		for _, p := range packages {
			var name string
			_ = json.Unmarshal(p["name"], &name)
			if target, ok := targets[name]; ok {
				p["version"], _ = json.Marshal(target)
			}
		}
		lock[section], _ = json.Marshal(packages)
	}
	b, e := json.Marshal(lock)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(ComposerLockFile, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func assertUnchangedProjectFields(t *testing.T, before, after manifest) {
	t.Helper()
	for key, raw := range before {
		if key == "require" || key == "require-dev" {
			continue
		}
		var old, new interface{}
		_ = json.Unmarshal(raw, &old)
		_ = json.Unmarshal(after[key], &new)
		if !reflect.DeepEqual(old, new) {
			t.Errorf("unrelated project field changed: %s", key)
		}
	}
}
func TestRealFixturesMixedMinorUpdates(t *testing.T) {
	for _, project := range projectFixtures(t) {
		t.Run(project, func(t *testing.T) {
			before, locked, original := copyProjectFixture(t, project)
			roots := requirements(before)
			targets := map[string]string{}
			skipped := 0
			updated := false
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "show" {
					name := args[1]
					current := locked[name]
					if !releaseVersion.MatchString(current) || strings.Contains(strings.ToLower(roots[name]), "dev") {
						t.Fatalf("queried a development branch: %s", name)
					}
					base := strings.SplitN(current, "-", 2)[0]
					v, ok := version(base)
					if !ok {
						t.Fatal(current)
					}
					target := fmt.Sprintf("%d.%d.%d", v[0], v[1]+1, 0)
					// Also offer a newer major to ensure minor mode does not select it.
					b, _ := json.Marshal(map[string]interface{}{"versions": []string{target, fmt.Sprintf("%d.0.0", v[0]+1), "dev-main"}})
					return b, "", nil
				}
				if args[0] != "update" {
					t.Fatalf("unexpected command %v", args)
				}
				updated = true
				for _, arg := range args[1:] {
					if !strings.HasPrefix(arg, "drupal/") {
						continue
					}
					name, target, ok := strings.Cut(arg, ":")
					if !ok {
						t.Fatal("missing temporary exact target")
					}
					targets[name] = target
				}
				writeFixtureResolution(t, original, targets)
				return nil, "", nil
			}
			if e := runMode("minor", "true", "", "report.md"); e != nil {
				t.Fatal(e)
			}
			if !updated {
				t.Fatal("no update performed")
			}
			after, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			assertUnchangedProjectFields(t, before, after)
			gotRoots := requirements(after)
			gotLocked, e := lockedPackages()
			if e != nil {
				t.Fatal(e)
			}
			for name, oldConstraint := range roots {
				target, selected := targets[name]
				if !selected {
					if gotRoots[name] != oldConstraint {
						t.Errorf("unselected constraint changed: %s", name)
					}
					if locked[name] != "" && gotLocked[name] != locked[name] {
						t.Errorf("unselected locked package changed: %s", name)
					}
					if strings.HasPrefix(name, "drupal/") && strings.Contains(strings.ToLower(oldConstraint), "dev") {
						skipped++
					}
					continue
				}
				want, e := modeConstraint(oldConstraint, target)
				if e != nil {
					t.Fatal(e)
				}
				if gotRoots[name] != want {
					t.Errorf("%s lost constraint style: %s", name, gotRoots[name])
				}
				oldVersion, _ := version(strings.SplitN(locked[name], "-", 2)[0])
				newVersion, _ := version(target)
				if oldVersion[0] != newVersion[0] {
					t.Errorf("minor mode crossed major for %s", name)
				}
			}
			if skipped == 0 {
				t.Fatal("fixture no longer exercises development branches")
			}
			report, e := os.ReadFile("report.md")
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(report), "Skipped") {
				t.Fatal("missing skipped branch explanation")
			}
		})
	}
}
func TestRealFixturesCoreMajorAndRollback(t *testing.T) {
	for _, project := range projectFixtures(t) {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", project, fail), func(t *testing.T) {
				before, locked, original := copyProjectFixture(t, project)
				roots := requirements(before)
				savedSelection := activeSelection
				activeSelection = selection{scope: "core"}
				t.Cleanup(func() { activeSelection = savedSelection })
				base := locked["drupal/core"]
				v, ok := version(base)
				if !ok {
					t.Fatal(base)
				}
				target := fmt.Sprintf("%d.0.0", v[0]+1)
				var selected []string
				runComposer = func(args ...string) ([]byte, string, error) {
					if args[0] == "show" {
						if !isCore(args[1]) {
							t.Fatal("selected contrib")
						}
						b, _ := json.Marshal(map[string]interface{}{"versions": []string{target}})
						return b, "", nil
					}
					updates := map[string]string{}
					for _, arg := range args[1:] {
						if !strings.HasPrefix(arg, "drupal/") {
							continue
						}
						name, release, _ := strings.Cut(arg, ":")
						selected = append(selected, name)
						updates[name] = release
					}
					writeFixtureResolution(t, original, updates)
					if fail {
						_ = os.WriteFile(ComposerJsonFile, []byte(`{"damaged":true}`), 0600)
						return nil, "fixture dependency conflict", composerExit(2)
					}
					return nil, "", nil
				}
				e := runMode("major", "true", "", "report.md")
				if (e != nil) != fail {
					t.Fatalf("unexpected result %v", e)
				}
				if fail {
					manifestBytes, _ := os.ReadFile(ComposerJsonFile)
					var restored manifest
					_ = json.Unmarshal(manifestBytes, &restored)
					if !reflect.DeepEqual(restored, before) {
						t.Fatal("manifest not restored exactly")
					}
					lockBytes, _ := os.ReadFile(ComposerLockFile)
					if string(lockBytes) != string(original) {
						t.Fatal("lock not restored exactly")
					}
					return
				}
				after, e := readManifest()
				if e != nil {
					t.Fatal(e)
				}
				assertUnchangedProjectFields(t, before, after)
				gotRoots := requirements(after)
				for _, name := range coreReleasePackages {
					if old, exists := roots[name]; exists {
						want, e := modeConstraint(old, target)
						if e != nil {
							t.Fatal(e)
						}
						if gotRoots[name] != want {
							t.Errorf("%s not coordinated: %s", name, gotRoots[name])
						}
						found := false
						for _, n := range selected {
							if n == name {
								found = true
							}
						}
						if !found {
							t.Errorf("%s absent from update command", name)
						}
					}
				}
				for name, old := range roots {
					if !isCore(name) && gotRoots[name] != old {
						t.Errorf("contrib changed: %s", name)
					}
				}
				if _, exists := before["require-dev"]; exists {
					var oldDev, newDev map[string]string
					_ = json.Unmarshal(before["require-dev"], &oldDev)
					_ = json.Unmarshal(after["require-dev"], &newDev)
					for name := range oldDev {
						if _, exists := newDev[name]; !exists {
							t.Errorf("development requirement moved: %s", name)
						}
					}
				}
			})
		}
	}
}
func TestRealFixtureNuclearPatchRemoval(t *testing.T) {
	before, _, original := copyProjectFixture(t, "project_x")
	var extra struct {
		Patches map[string]map[string]string `json:"patches"`
	}
	if e := json.Unmarshal(before["extra"], &extra); e != nil {
		t.Fatal(e)
	}
	var packages []string
	for n := range extra.Patches {
		packages = append(packages, n)
	}
	sort.Strings(packages)
	if len(packages) == 0 {
		t.Fatal("patch fixture missing")
	}
	name := packages[0]
	var descriptions []string
	for d := range extra.Patches[name] {
		descriptions = append(descriptions, d)
	}
	sort.Strings(descriptions)
	description := descriptions[0]
	patchURL := extra.Patches[name][description]
	attempts := 0
	runComposer = func(args ...string) ([]byte, string, error) {
		attempts++
		if attempts == 1 {
			return nil, "Could not apply patch " + patchURL, composerExit(1)
		}
		writeFixtureResolution(t, original, nil)
		return nil, "", nil
	}
	if e := runMode("nuclear", "true", "", "report.md"); e != nil {
		t.Fatal(e)
	}
	after, e := readManifest()
	if e != nil {
		t.Fatal(e)
	}
	delete(extra.Patches[name], description)
	var actual struct {
		Patches map[string]map[string]string `json:"patches"`
	}
	if e = json.Unmarshal(after["extra"], &actual); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(extra.Patches, actual.Patches) {
		t.Fatal("removed incorrect patch or modified other patches")
	}
	if !reflect.DeepEqual(requirements(before), requirements(after)) {
		t.Fatal("patch recovery changed package constraints")
	}
}

func TestRealFixturesSecurityTargetsIncludeTransitiveDrupal(t *testing.T) {
	for _, project := range projectFixtures(t) {
		t.Run(project, func(t *testing.T) {
			before, locked, original := copyProjectFixture(t, project)
			roots := requirements(before)
			var transitive []string
			for n := range locked {
				if strings.HasPrefix(n, "drupal/") && !isCore(n) {
					if _, direct := roots[n]; !direct {
						transitive = append(transitive, n)
					}
				}
			}
			sort.Strings(transitive)
			if len(transitive) == 0 {
				t.Fatal("fixture no longer includes transitive Drupal packages")
			}
			affectedName := transitive[0]
			audits := 0
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "audit" {
					audits++
					if audits == 2 {
						return []byte(`{"advisories":[]}`), "", nil
					}
					b, _ := json.Marshal(map[string]interface{}{"advisories": map[string]interface{}{affectedName: []interface{}{map[string]string{"title": "controlled fixture advisory"}}}})
					return b, "", composerExit(1)
				}
				if args[0] != "update" || len(args) < 2 || args[1] != affectedName {
					t.Fatalf("wrong security update targets: %v", args)
				}
				for _, arg := range args[2:] {
					if strings.HasPrefix(arg, "drupal/") {
						t.Fatalf("selected unaffected package %s", arg)
					}
				}
				writeFixtureResolution(t, original, nil)
				return nil, "", nil
			}
			if e := runMode("security", "true", "", "report.md"); e != nil {
				t.Fatal(e)
			}
			after, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("security mode rewrote the full project manifest")
			}
		})
	}
}

func TestRealFixturePrereleaseToStable(t *testing.T) {
	before, locked, _ := copyProjectFixture(t, "project_x")
	roots := requirements(before)
	tested := 0
	for name, current := range locked {
		if _, direct := roots[name]; !direct || !releaseVersion.MatchString(current) || !strings.Contains(current, "-") {
			continue
		}
		stable := strings.SplitN(current, "-", 2)[0]
		target, e := targetVersion(current, []string{stable}, "minor")
		if e != nil || target != stable {
			t.Errorf("%s: %s → %s: %v", name, current, target, e)
		}
		tested++
	}
	if tested == 0 {
		t.Fatal("fixture no longer includes numeric prereleases")
	}
	if developmentConstraint("^1.0@dev") {
		t.Fatal("stability allowance incorrectly treated as a pinned branch")
	}
	if !developmentConstraint("1.x-dev@dev") {
		t.Fatal("pinned branch not detected")
	}
}
