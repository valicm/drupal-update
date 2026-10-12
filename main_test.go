package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		fail       bool
		wantCalls  int
	}{
		{"safe", SemverSafeUpdate, false, 1},
		{"all", UpdateAll, false, 2},
		{"failed", SemverSafeUpdate, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			original := runComposer
			t.Cleanup(func() { runComposer = original; _ = os.Chdir(old) })
			t.Setenv("GITHUB_ACTIONS", "true")
			t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(dir, "step"))
			t.Setenv("GITHUB_ENV", filepath.Join(dir, "env"))
			if err := os.WriteFile("composer.json", []byte(`{"require":{"drupal/token":"^1"},"require-dev":{"drupal/devel":"^4"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "outdated" {
					return []byte(`{"locked":[{"name":"drupal/token","version":"1.0","latest":"1.1","latest-status":"semver-safe-update","abandoned":false},{"name":"drupal/devel","version":"4.0","latest":"5.0","latest-status":"update-possible"}]}`), "warning on stderr", nil
				}
				calls = append(calls, args)
				if tc.fail {
					return nil, "Could not apply patch", errors.New("exit 1")
				}
				return nil, "", nil
			}
			err = updateProjects(tc.mode, "true", "", "report.md")
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(calls) != tc.wantCalls {
				t.Fatalf("calls: %v", calls)
			}
			if tc.mode == UpdateAll && !strings.Contains(strings.Join(calls[1], " "), "--dev") {
				t.Fatal("lost development requirement")
			}
			report, err := os.ReadFile("report.md")
			if err != nil {
				t.Fatal(err)
			}
			if tc.fail && !strings.Contains(string(report), PatchFailure) {
				t.Fatal("missing patch failure")
			}
			env, err := os.ReadFile("env")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(env), "DRUPAL_UPDATES_TABLE<<") {
				t.Fatal("missing GitHub output")
			}
		})
	}
}
