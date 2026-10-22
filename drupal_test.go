package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDrupalLifecycleOrderAndFailures(t *testing.T) {
	for _, failure := range []string{"", "site:install", "composer", "updatedb", "config:export"} {
		t.Run("failure="+failure, func(t *testing.T) {
			modeFixture(t, `{}`, `{"packages":[]}`)
			oldDrush := runDrush
			t.Cleanup(func() { runDrush = oldDrush })
			var calls []string
			runDrush = func(command []string, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(command, []string{"/bin/true", "--uri=https://test.invalid"}) {
					t.Fatalf("launcher: %v", command)
				}
				calls = append(calls, args[0])
				want := []string{args[0], "--yes"}
				if args[0] == "site:install" {
					want = []string{"site:install", "--existing-config", "--yes"}
				}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("arguments: %v", args)
				}
				if failure == args[0] {
					return []byte("Drush diagnostic"), errors.New("exit status 1")
				}
				return nil, nil
			}
			t.Setenv("GITHUB_ACTIONS", "true")
			t.Setenv("GITHUB_STEP_SUMMARY", "summary.md")
			t.Setenv("GITHUB_ENV", "env.txt")
			err := (drupalLifecycle{true, true, true, "/bin/true --uri=https://test.invalid"}).run("report.md", func() error {
				calls = append(calls, "composer")
				if err := os.WriteFile("composer.lock", []byte("upgraded"), 0600); err != nil {
					t.Fatal(err)
				}
				_ = publishReport("Composer report\n", "report.md")
				if failure == "composer" {
					return errors.New("composer failed")
				}
				return nil
			})
			expected := []string{"site:install", "composer", "updatedb", "config:export"}
			if failure != "" {
				for i, c := range expected {
					if c == failure {
						expected = expected[:i+1]
						break
					}
				}
			}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("calls %v, want %v", calls, expected)
			}
			if (err != nil) != (failure != "") {
				t.Fatalf("error %v", err)
			}
			report, _ := os.ReadFile("report.md")
			summary, _ := os.ReadFile("summary.md")
			env, _ := os.ReadFile("env.txt")
			if string(report) != string(summary) || !strings.Contains(string(env), string(report)) {
				t.Fatal("inconsistent report destinations")
			}
			if failure != "site:install" && !strings.Contains(string(report), "Composer report") {
				t.Fatal("missing Composer report")
			}
			if failure == "updatedb" || failure == "config:export" {
				lock, _ := os.ReadFile("composer.lock")
				if string(lock) != "upgraded" {
					t.Fatal("post-upgrade failure reverted Composer lock")
				}
				if !strings.Contains(string(report), "Drush diagnostic") || !strings.Contains(string(report), "No database rollback") {
					t.Fatal(string(report))
				}
			}
		})
	}
}

func TestDrupalLifecycleDisabledAndExistingDatabase(t *testing.T) {
	old := runDrush
	t.Cleanup(func() { runDrush = old })
	var calls []string
	runDrush = func(_ []string, args ...string) ([]byte, error) { calls = append(calls, args[0]); return nil, nil }
	for _, enabled := range []bool{false, true} {
		calls = nil
		d := drupalLifecycle{updateDB: enabled, export: enabled, command: "/bin/true"}
		if err := d.run("", func() error { calls = append(calls, "composer"); return nil }); err != nil {
			t.Fatal(err)
		}
		want := []string{"composer"}
		if enabled {
			want = append(want, "updatedb", "config:export")
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatal(calls)
		}
	}
}

func TestDrupalLifecycleValidationBeforeUpgrade(t *testing.T) {
	for _, d := range []drupalLifecycle{
		{export: true, command: "/bin/true"},
		{updateDB: true, command: "'unterminated"},
		{install: true, command: "/missing/drush"},
	} {
		called := false
		err := d.run("", func() error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("validation %v called %v", err, called)
		}
	}
}

func TestDrupalLifecycleActionInputs(t *testing.T) {
	modeFixture(t, `{}`, `{"packages":[]}`)
	for key, value := range map[string]string{
		"INPUT_INSTALL_EXISTING_CONFIG": "true", "INPUT_UPDATEDB": "true", "INPUT_EXPORT_CONFIG": "true", "INPUT_DRUSH_COMMAND": "/bin/true",
	} {
		t.Setenv(key, value)
	}
	old := runDrush
	t.Cleanup(func() { runDrush = old })
	var calls []string
	runDrush = func(_ []string, args ...string) ([]byte, error) { calls = append(calls, args[0]); return nil, nil }
	runComposer = func(args ...string) ([]byte, string, error) { return []byte(`{"advisories":[]}`), "", nil }
	if err := newApp().Run([]string{"drupal-update", "-t", "security", "--composer-command", "/bin/true"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"site:install", "updatedb", "config:export"}) {
		t.Fatal(calls)
	}
}
