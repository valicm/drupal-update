package main

import (
	"errors"
	"io"
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

func TestStdoutCarriesOnlyTheReportAndActionOutputs(t *testing.T) {
	for _, newer := range []bool{true, false} {
		t.Run(map[bool]string{true: "changed", false: "unchanged"}[newer], func(t *testing.T) {
			modeFixture(t, `{"require":{"drupal/token":"^1"}}`, `{"packages":[{"name":"drupal/token","version":"1.0.0"}]}`)
			dir, e := os.Getwd()
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("GITHUB_ACTIONS", "true")
			for _, name := range []string{"GITHUB_STEP_SUMMARY", "GITHUB_ENV", "GITHUB_OUTPUT"} {
				t.Setenv(name, filepath.Join(dir, name))
			}
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "show" {
					if newer {
						return []byte(`{"versions":["1.1.0","1.0.0"]}`), "", nil
					}
					return []byte(`{"versions":["1.0.0"]}`), "", nil
				}
				return []byte("Composer output"), "Composer progress", os.WriteFile(ComposerLockFile, []byte(`{"packages":[{"name":"drupal/token","version":"1.1.0"}]}`), 0600)
			}
			r, w, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			stdout := os.Stdout
			os.Stdout = w
			t.Cleanup(func() { os.Stdout = stdout })
			e = newApp().Run([]string{"drupal-update", "--composer-command", "/bin/true", "-t", "minor", "-o", "report.md"})
			os.Stdout = stdout
			_ = w.Close()
			if e != nil {
				t.Fatal(e)
			}
			printed, _ := io.ReadAll(r)
			report, _ := os.ReadFile("report.md")
			if len(report) == 0 || string(printed) != string(report) {
				t.Fatalf("stdout is not the report:\n%s", printed)
			}
			outputs, _ := os.ReadFile(filepath.Join(dir, "GITHUB_OUTPUT"))
			if !strings.Contains(string(outputs), "report<<") || !strings.Contains(string(outputs), string(report)) {
				t.Fatalf("report output missing:\n%s", outputs)
			}
			if want := "changed=" + map[bool]string{true: "true", false: "false"}[newer] + "\n"; !strings.Contains(string(outputs), want) {
				t.Fatalf("want %q in:\n%s", want, outputs)
			}
		})
	}
}
