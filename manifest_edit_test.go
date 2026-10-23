package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const formattedManifest = `{
    "name": "acme/site",
    "type": "project",
    "require": {
        "php": ">=8.3",
        "drupal/core-recommended": "^11.2",
        "drupal/token": "^1.13"
    },
    "require-dev": {
        "drupal/core-dev": "^11.2"
    },
    "extra": {
        "patches": {
            "drupal/token": {
                "Fix <br> & tokens": "https://example.test/first.patch",
                "Apply after the first": "https://example.test/second.patch"
            },
            "drupal/redirect": [
                {"description": "one", "url": "https://example.test/one.patch"},
                {"description": "two", "url": "https://example.test/two.patch"}
            ]
        }
    }
}
`

func readComposerJSON(t *testing.T) string {
	t.Helper()
	b, e := os.ReadFile(ComposerJsonFile)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestConstraintEditsKeepFormatting(t *testing.T) {
	modeFixture(t, formattedManifest, `{"packages":[]}`)
	m, e := readManifest()
	if e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]string{"drupal/token": "^1.16.0", "drupal/core-dev": "^11.3.0", "drupal/core-recommended": "^11.2"} {
		if e = setConstraint(m, name, value); e != nil {
			t.Fatal(e)
		}
	}
	want := strings.NewReplacer(`"drupal/token": "^1.13"`, `"drupal/token": "^1.16.0"`, `"drupal/core-dev": "^11.2"`, `"drupal/core-dev": "^11.3.0"`).Replace(formattedManifest)
	if got := readComposerJSON(t); got != want {
		t.Fatalf("composer.json changed beyond the constraints:\n%s", got)
	}
	if requirements(m)["drupal/core-dev"] != "^11.3.0" {
		t.Fatal("manifest not updated in memory")
	}
}

func TestPatchRemovalKeepsOrderAndFormatting(t *testing.T) {
	for _, tc := range []struct{ name, log, removed string }{
		{"legacy first", "No available patcher was able to apply patch https://example.test/first.patch to drupal/token", `
                "Fix <br> & tokens": "https://example.test/first.patch",`},
		{"expanded last", "Could not apply patch! Skipping. The error was: Cannot apply patch https://example.test/two.patch", `,
                {"description": "two", "url": "https://example.test/two.patch"}`},
		{"expanded first", "Could not apply patch https://example.test/one.patch", `
                {"description": "one", "url": "https://example.test/one.patch"},`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modeFixture(t, formattedManifest, `{"packages":[]}`)
			m, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			removed, e := removeFailedPatch(m, tc.log)
			if e != nil || removed == "" {
				t.Fatalf("%q %v", removed, e)
			}
			if got, want := readComposerJSON(t), strings.Replace(formattedManifest, tc.removed, "", 1); got != want {
				t.Fatalf("got:\n%s", got)
			}
			after, _ := readManifest()
			if string(after["extra"]) == "" || !json.Valid([]byte(readComposerJSON(t))) {
				t.Fatal("invalid composer.json")
			}
		})
	}
}

func TestRemovingTheLastMemberLeavesAnEmptyContainer(t *testing.T) {
	modeFixture(t, `{"extra": {"patches": {"drupal/token": {"only": "https://example.test/only.patch"}}}}`, `{"packages":[]}`)
	if e := removeManifestMember(func(_ int, key string) bool { return key == "only" }, "extra", "patches", "drupal/token"); e != nil {
		t.Fatal(e)
	}
	if got := readComposerJSON(t); got != `{"extra": {"patches": {"drupal/token": {}}}}` {
		t.Fatal(got)
	}
}

func TestComposerPatches2FailureWrappedAcrossLines(t *testing.T) {
	url := "https://www.drupal.org/files/issues/2026-01-01/3412345-12-a-very-long-patch-name.patch"
	log := "In Patches.php line 288:\n\n  No available patcher was able to apply patch https://www.drupal.org/files/issues/  \n  2026-01-01/3412345-12-a-very-long-patch-name.patch to drupal/core              \n"
	if !isPatchFailure(log) {
		t.Fatal("2.x patch failure not recognised")
	}
	patches := map[string][]string{"drupal/core": {url}, "drupal/token": {"https://example.test/token.patch"}}
	if got := failedPatchTarget(log, patches, []string{"drupal/core-recommended", "drupal/token"}); got != "drupal/core" {
		t.Fatalf("identified %q", got)
	}
}

func TestConfiguredPatchSources(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		files          map[string]string
		want           []string
	}{
		{"inline and 1.x file", `{"extra":{"patches":{"drupal/token":{"fix":"https://example.test/token.patch"}},"patches-file":"legacy.json"}}`,
			map[string]string{"legacy.json": `{"patches":{"drupal/redirect":{"fix":"https://example.test/redirect.patch"}}}`},
			[]string{"drupal/token", "drupal/redirect"}},
		{"2.x default patches.json", `{}`,
			map[string]string{"patches.json": `{"patches":{"drupal/redirect":[{"description":"fix","url":"https://example.test/redirect.patch"}]}}`},
			[]string{"drupal/redirect"}},
		{"2.x configured file and lock", `{"extra":{"composer-patches":{"patches-file":"patches/list.json"}}}`,
			map[string]string{"patches/list.json": `{"patches":{"drupal/token":[{"url":"https://example.test/token.patch"}]}}`,
				"patches.lock.json": `{"_hash":"x","patches":{"drupal/gin":[{"package":"drupal/gin","url":"https://example.test/gin.patch","extra":{"provenance":"dependency:drupal/theme"}}]}}`},
			[]string{"drupal/token", "drupal/gin"}},
		{"2.x configured file missing", `{"extra":{"composer-patches":{"patches-file":"missing.json"}}}`, nil, nil},
		{"unknown definition shape", `{"extra":{"patches":{"drupal/token":{"fix":{"url":"https://example.test/token.patch"}}}}}`, nil, []string{"drupal/token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modeFixture(t, tc.manifest, `{"packages":[]}`)
			for path, text := range tc.files {
				if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte(text), 0600); e != nil {
					t.Fatal(e)
				}
			}
			m, e := readManifest()
			if e != nil {
				t.Fatal(e)
			}
			patches, e := configuredPatches(m)
			if e != nil {
				t.Fatal(e)
			}
			for _, name := range tc.want {
				if !hasPatches(name, patches) {
					t.Fatalf("%s not patched: %v", name, patches)
				}
			}
			if len(patches) != len(tc.want) {
				t.Fatalf("unexpected packages: %v", patches)
			}
		})
	}
}

func TestBackupIgnoresItself(t *testing.T) {
	modeFixture(t, `{}`, `{"packages":[]}`)
	backup, e := backupProject()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(backup.dir, ".gitignore"))
	if e != nil || string(b) != "*\n" {
		t.Fatalf("%q %v", b, e)
	}
}
