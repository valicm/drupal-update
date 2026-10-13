package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConstraintStyles(t *testing.T) {
	for _, tc := range []struct{ old, target, want string }{{"^1.0", "2.5.0", "^2.5.0"}, {"~1.0.0", "2.5.0", "~2.5.0"}, {"^1.0@beta", "2.5.0", "^2.5.0@beta"}, {"1.0.0", "2.5.0", "2.5.0"}, {"1.*", "2.5.0", "2.*"}, {"1.2.x", "2.5.0", "2.5.x"}, {"*", "2.5.0", "*"}} {
		got, e := upgradeConstraint(tc.old, tc.target)
		if e != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.old, got, e)
		}
	}
	if _, e := upgradeConstraint("^1 || ^2", "3.0.0"); e == nil {
		t.Fatal("silently replaced complex constraint")
	}
}
func TestLauncherParsing(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{{"ddev composer", []string{"ddev", "composer"}}, {`docker compose exec -T php composer`, []string{"docker", "compose", "exec", "-T", "php", "composer"}}, {`'/path with spaces/composer' --some-flag`, []string{"/path with spaces/composer", "--some-flag"}}, {`composer '$(echo unsafe)'`, []string{"composer", "$(echo unsafe)"}}} {
		got, e := splitCommand(tc.command)
		if e != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %v %v", tc.command, got, e)
		}
	}
	for _, bad := range []string{"", `composer 'unterminated`, `composer \`} {
		if _, e := splitCommand(bad); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestSelection(t *testing.T) {
	for _, tc := range []struct {
		scope, packages, name string
		want                  bool
	}{{"core", "", "drupal/token", false}, {"core", "", "drupal/core-dev", true}, {"contrib", "", "drupal/core", false}, {"contrib", "token", "drupal/token", true}, {"all", "token", "drupal/redirect", false}, {"all", "core-recommended", "drupal/core-dev", true}} {
		s, e := parseSelection(tc.scope, tc.packages)
		if e != nil || s.includes(tc.name) != tc.want {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
	for _, tc := range [][2]string{{"other", ""}, {"core", "token"}, {"contrib", "core"}, {"all", "--invalid"}} {
		if _, e := parseSelection(tc[0], tc[1]); e == nil {
			t.Fatal(tc)
		}
	}
}
func TestCLIContainerLauncherWithoutHostPHP(t *testing.T) {
	modeFixture(t, `{"require":{}}`, `{"packages":[]}`)
	dir, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	launcher := filepath.Join(dir, "container launcher")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > launcher-args\nprintf '%s\\n' '{\"locked\":[]}'\n"
	if e = os.WriteFile(launcher, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir)
	t.Setenv("INPUT_COMPOSER_COMMAND", "does-not-exist")
	t.Setenv("INPUT_UPDATE_SCOPE", "contrib")
	if e = newApp().Run([]string{"drupal-update", "--composer-command", "'" + launcher + "' composer", "--scope", "core"}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("launcher-args")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(b), "composer\noutdated\n") {
		t.Fatal(string(b))
	}
	if composerCommand[0] != "composer" || activeSelection.scope != "all" {
		t.Fatal("CLI config leaked")
	}
}
func TestReportLinks(t *testing.T) {
	if got := releaseLink("drupal/core-recommended", "11.2.0"); !strings.Contains(got, "/project/drupal/releases/11.2.0") {
		t.Fatal(got)
	}
	if got := releaseLink("drupal/token", "dev-main"); got != "dev-main" {
		t.Fatal(got)
	}
	if got := packageLink("drupal/token"); !strings.Contains(got, "/project/token") {
		t.Fatal(got)
	}
}

func TestScopeAndPackageSelectionThroughCLI(t *testing.T) {
	for _, tc := range []struct{ scope, packages, mode, want string }{{"core", "", SemverSafeUpdate, "drupal/core"}, {"contrib", "", SemverSafeUpdate, "drupal/token"}, {"all", "token", SemverSafeUpdate, "drupal/token"}, {"core", "", "minor", "drupal/core-recommended"}, {"contrib", "token", "minor", "drupal/token"}} {
		t.Run(tc.scope+tc.packages+tc.mode, func(t *testing.T) {
			modeFixture(t, `{"require":{"drupal/core-recommended":"^10.0","drupal/token":"^1.0","drupal/redirect":"^1.0"}}`, `{"packages":[{"name":"drupal/core-recommended","version":"10.0.0"},{"name":"drupal/token","version":"1.0.0"}]}`)
			t.Setenv("INPUT_UPDATE_SCOPE", "")
			t.Setenv("INPUT_UPDATE_PACKAGES", "")
			t.Setenv("INPUT_COMPOSER_COMMAND", "")
			var updated []string
			runComposer = func(args ...string) ([]byte, string, error) {
				if args[0] == "outdated" {
					return []byte(`{"locked":[{"name":"drupal/core-recommended","version":"10.0.0","latest":"10.1.0","latest-status":"semver-safe-update"},{"name":"drupal/token","version":"1.0.0","latest":"1.1.0","latest-status":"semver-safe-update"}]}`), "", nil
				}
				if args[0] == "show" {
					if args[1] == "drupal/core-recommended" {
						return []byte(`{"versions":["10.1.0"]}`), "", nil
					}
					return []byte(`{"versions":["1.1.0"]}`), "", nil
				}
				updated = append(updated, strings.Join(args, " "))
				return nil, "", os.WriteFile(ComposerLockFile, []byte(`{"packages":[{"name":"drupal/core-recommended","version":"10.1.0"},{"name":"drupal/token","version":"1.1.0"}]}`), 0600)
			}
			if e := newApp().Run([]string{"drupal-update", "--composer-command", "/bin/true", "--type", tc.mode, "--scope", tc.scope, "--packages", tc.packages}); e != nil {
				t.Fatal(e)
			}
			if len(updated) != 1 || !strings.Contains(updated[0], tc.want) {
				t.Fatalf("commands: %v", updated)
			}
			if tc.scope == "core" && strings.Contains(updated[0], "drupal/token") {
				t.Fatal("selected contrib during core update")
			}
			if tc.want == "drupal/token" && strings.Contains(updated[0], "drupal/core") {
				t.Fatal("selected core during module update")
			}
		})
	}
}
