package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/urfave/cli/v2"
	"os"
	"os/exec"
	"reflect"
	"strings"
)

const (
	Success           = "success"
	PatchFailure      = "patch failure"
	GenericError      = "generic error"
	DependencyError   = "failed dependency"
	Unknown           = "unknown"
	Skipped           = "skipped"
	SemverSafeUpdate  = "semver-safe-update"
	UpdateAll         = "all"
	DefaultUpdateType = SemverSafeUpdate
	DefaultCore       = "true"
	DefaultOutput     = ""
	DefaultExclude    = ""
	ComposerJsonFile  = "composer.json"
	ComposerLockFile  = "composer.lock"
)

type Update struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Latest    string `json:"latest"`
	Status    string `json:"latest-status"`
	Homepage  string `json:"homepage"`
	Abandoned any    `json:"abandoned"`
}

type Outdated struct {
	Packages []Update `json:"locked"`
}

var buildVersion = "dev"

func newApp() *cli.App {
	return &cli.App{
		Name:    "Drupal Update",
		Version: buildVersion,
		Usage:   "A tool to perform composer updates of Drupal projects",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "install-existing-config", EnvVars: []string{"INPUT_INSTALL_EXISTING_CONFIG"}, Usage: "Install current locked Drupal from config BEFORE upgrading (disposable database only)"},
			&cli.BoolFlag{Name: "updatedb", EnvVars: []string{"INPUT_UPDATEDB"}, Usage: "Run Drush updatedb after successful Composer upgrades"},
			&cli.BoolFlag{Name: "export-config", EnvVars: []string{"INPUT_EXPORT_CONFIG"}, Usage: "Run Drush config:export after successful Composer upgrades and database updates"},
			&cli.StringFlag{Name: "drush-command", Value: "vendor/bin/drush", EnvVars: []string{"INPUT_DRUSH_COMMAND"}, Usage: "Drush launcher, e.g. ddev drush (no shell expansion)"},
			&cli.BoolFlag{Name: "security-update-pinned", EnvVars: []string{"INPUT_SECURITY_UPDATE_PINNED"}, Usage: "Allow security fixes to advance exact pins within the current minor version"},
			&cli.BoolFlag{Name: "skip-patched", EnvVars: []string{"INPUT_SKIP_PATCHED"}, Usage: "Skip update targets with configured inline or external patches"},
			&cli.StringFlag{Name: "patch-failure", Value: "fail", EnvVars: []string{"INPUT_PATCH_FAILURE"}, Usage: "Patch failure policy: fail or skip (minor/major only)"},
			&cli.StringFlag{Name: "composer-command", Value: "composer", EnvVars: []string{"INPUT_COMPOSER_COMMAND"}, Usage: "Composer launcher, e.g. ddev composer (no shell expansion)"},
			&cli.StringFlag{Name: "scope", Value: "all", EnvVars: []string{"INPUT_UPDATE_SCOPE"}, Usage: "Update targets: all, core or contrib"},
			&cli.StringFlag{Name: "packages", EnvVars: []string{"INPUT_UPDATE_PACKAGES"}, Usage: "Comma-separated package or module names to update"},
			&cli.StringFlag{
				Name:    "type",
				Aliases: []string{"t"},
				Value:   DefaultUpdateType,
				EnvVars: []string{"INPUT_UPDATE_TYPE"},
				Usage:   "Update type: security, minor, major, nuclear, semver-safe-update or all",
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Value:   DefaultOutput,
				EnvVars: []string{"INPUT_OUTPUT_FILE"},
				Usage:   "Specify an output file to save summary",
			},
			&cli.StringFlag{
				Name:    "core",
				Aliases: []string{"c"},
				Value:   DefaultCore,
				EnvVars: []string{"INPUT_UPDATE_CORE"},
				Usage:   "Flag to enable or disable Drupal core upgrades check",
			},
			&cli.StringFlag{
				Name:    "exclude",
				Aliases: []string{"e"},
				Value:   DefaultExclude,
				EnvVars: []string{"INPUT_UPDATE_EXCLUDE"},
				Usage:   "Exclude certain modules from updates check",
			},
		},
		Action: func(c *cli.Context) error {
			updateType := c.String("type")
			output := c.String("output")
			core := c.String("core")
			exclude := c.String("exclude")

			// Validate options
			if err := validateOptions(updateType, core, output); err != nil {
				return err
			}

			if c.Args().Len() > 0 {
				return fmt.Errorf("unexpected arguments: %s", strings.Join(c.Args().Slice(), " "))
			}
			launcher, err := splitCommand(c.String("composer-command"))
			if err != nil {
				return err
			}
			selection, err := parseSelection(c.String("scope"), c.String("packages"))
			if err != nil {
				return err
			}
			if selection.scope == "core" && core == "false" {
				return fmt.Errorf("scope=core conflicts with core=false")
			}
			if updateType == "nuclear" && (selection.scope != "all" || len(selection.packages) > 0) {
				return fmt.Errorf("nuclear cannot restrict scope or packages")
			}
			policy := c.String("patch-failure")
			if policy != "fail" && policy != "skip" {
				return fmt.Errorf("patch-failure must be fail or skip")
			}
			if policy == "skip" && updateType != "minor" && updateType != "major" {
				return fmt.Errorf("patch-failure=skip is supported for minor and major modes")
			}
			if updateType == "nuclear" && c.Bool("skip-patched") {
				return fmt.Errorf("nuclear cannot skip patched packages")
			}
			if c.Bool("security-update-pinned") && updateType != "security" {
				return fmt.Errorf("security-update-pinned requires type=security")
			}
			previousPinned := securityUpdatePinned
			securityUpdatePinned = c.Bool("security-update-pinned")
			defer func() { securityUpdatePinned = previousPinned }()
			previousSkip, previousPolicy := skipPatched, patchFailurePolicy
			skipPatched, patchFailurePolicy = c.Bool("skip-patched"), policy
			defer func() { skipPatched, patchFailurePolicy = previousSkip, previousPolicy }()
			previousCommand, previousSelection := composerCommand, activeSelection
			composerCommand, activeSelection = launcher, selection
			defer func() { composerCommand, activeSelection = previousCommand, previousSelection }()
			// Validate requirements
			if err := validateRequirements(); err != nil {
				return err
			}

			drupal := drupalLifecycle{
				install: c.Bool("install-existing-config"), updateDB: c.Bool("updatedb"),
				export: c.Bool("export-config"), command: c.String("drush-command"),
			}
			before := trackedFiles()
			err = drupal.run(output, func() error {
				return updateProjects(updateType, core, exclude, output)
			})
			if os.Getenv("GITHUB_ACTIONS") == "true" {
				err = errors.Join(err, appendReport(os.Getenv("GITHUB_OUTPUT"), fmt.Sprintf("changed=%t\n", !reflect.DeepEqual(before, trackedFiles()))))
			}
			return err
		},
	}
}

func main() {
	err := newApp().Run(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func validateOptions(updateType, core, output string) error {
	if updateType != SemverSafeUpdate && updateType != UpdateAll && updateType != "security" && updateType != "minor" && updateType != "major" && updateType != "nuclear" {
		return fmt.Errorf("invalid update type %q", updateType)
	}

	if core != "true" && core != "false" {
		return fmt.Errorf("error: Core flag must be either true or false. Default if empty is true")
	}

	if output != "" && !strings.HasSuffix(output, ".md") {
		return fmt.Errorf("error: Summary output file needs to end with .md extension")
	}

	return nil
}

func validateRequirements() error {
	if _, err := os.Stat(ComposerJsonFile); os.IsNotExist(err) {
		return fmt.Errorf("error: %s is missing", ComposerJsonFile)
	}

	if _, err := os.Stat(ComposerLockFile); os.IsNotExist(err) {
		return fmt.Errorf("error: %s is missing", ComposerLockFile)
	}

	if len(activeSelection.packages) > 0 {
		locked, err := lockedPackages()
		if err != nil {
			return err
		}
		for name := range activeSelection.packages {
			if _, ok := locked[name]; ok {
				continue
			}
			found := false
			if isCore(name) {
				for n := range locked {
					if isCore(n) {
						found = true
						break
					}
				}
			}
			if !found {
				return fmt.Errorf("selected package %s is not in composer.lock", name)
			}
		}
	}
	binaries := []string{composerCommand[0]}
	for _, binary := range binaries {
		path, err := exec.LookPath(binary)
		if err != nil {
			return fmt.Errorf("error: %s is not installed", binary)
		}
		fmt.Fprintf(os.Stderr, "%s is available at %s\n", binary, path)
	}

	return nil
}

// runComposer keeps JSON stdout separate from diagnostic stderr.
var runComposer = func(args ...string) ([]byte, string, error) {
	cmd := exec.Command(composerCommand[0], append(append([]string{}, composerCommand[1:]...), args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}

func updateProjects(updateType, core, exclude, output string) error {
	if updateType == "security" || updateType == "minor" || updateType == "major" || updateType == "nuclear" {
		return runMode(updateType, core, exclude, output)
	}
	content, err := os.ReadFile(ComposerJsonFile)
	if err != nil {
		return err
	}
	m, e := readManifest()
	if e != nil {
		return e
	}
	patches, e := configuredPatches(m)
	if e != nil {
		return e
	}
	var project struct {
		Require    map[string]json.RawMessage `json:"require"`
		RequireDev map[string]json.RawMessage `json:"require-dev"`
		Extra      struct {
			Patches map[string]json.RawMessage `json:"patches"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(content, &project); err != nil {
		return fmt.Errorf("parse composer.json: %w", err)
	}
	data, diagnostics, err := runComposer("outdated", "drupal/*", "--format=json", "--direct", "--locked", "--no-interaction")
	if err != nil {
		return fmt.Errorf("discover updates: %w: %s", err, diagnostics)
	}
	var outdated Outdated
	if err := json.Unmarshal(data, &outdated); err != nil {
		return fmt.Errorf("parse outdated packages: %w", err)
	}
	// Core packages form one update group, performed after contributed packages.
	var packages []Update
	var coreUpdate *Update
	for _, pkg := range outdated.Packages {
		if isCore(pkg.Name) {
			if coreUpdate == nil || pkg.Name == "drupal/core-recommended" {
				copy := pkg
				coreUpdate = &copy
			}
		} else {
			packages = append(packages, pkg)
		}
	}
	if coreUpdate != nil {
		packages = append(packages, *coreUpdate)
	}
	var summary strings.Builder
	summary.WriteString("### Automated Drupal update summary\n\n| Package | Old version | Proposed version | Status | Abandoned |\n| --- | --- | --- | --- | --- |\n")
	var failures []error
	for _, pkg := range packages {
		status := Skipped
		skip := (skipPatched && hasPatches(pkg.Name, patches)) || excludedPackage(pkg.Name, core, exclude) || (updateType != UpdateAll && pkg.Status != SemverSafeUpdate)
		if !skip {
			args := []string{"update", pkg.Name}
			if isCore(pkg.Name) {
				args = []string{"update", "drupal/core", "drupal/core-*"}
			}
			if pkg.Status == "update-possible" {
				args = []string{"require"}
				if isCore(pkg.Name) {
					for _, name := range coreReleasePackages {
						if _, ok := project.Require[name]; ok {
							var previous string
							_ = json.Unmarshal(project.Require[name], &previous)
							constraint, err := upgradeConstraint(previous, pkg.Latest)
							if err != nil {
								return fmt.Errorf("%s: %w", name, err)
							}
							args = append(args, name+":"+constraint)
						}
					}
					// Do not accidentally move development requirements into require.
					if _, ok := project.RequireDev["drupal/core-dev"]; ok {
						failures = append(failures, fmt.Errorf("core major update with core-dev in require-dev requires coordinated constraint changes"))
						status = DependencyError
						skip = true
					}
				} else {
					raw := project.Require[pkg.Name]
					if raw == nil {
						raw = project.RequireDev[pkg.Name]
					}
					var previous string
					_ = json.Unmarshal(raw, &previous)
					constraint, err := upgradeConstraint(previous, pkg.Latest)
					if err != nil {
						return fmt.Errorf("%s: %w", pkg.Name, err)
					}
					args = append(args, pkg.Name+":"+constraint)
					if _, ok := project.RequireDev[pkg.Name]; ok {
						args = append(args, "--dev")
					}
				}
			}
			if !skip {
				args = append(args, "--with-all-dependencies", "--no-interaction", "--no-progress")
				fmt.Fprintf(os.Stderr, "Updating %s from %s towards %s\n", pkg.Name, pkg.Version, pkg.Latest)
				stdout, stderr, updateErr := runComposer(args...)
				if updateErr == nil {
					status = Success
				} else {
					status = GenericError
					var exit *exec.ExitError
					if errors.As(updateErr, &exit) && exit.ExitCode() == 2 {
						status = DependencyError
					}
					log := cleanComposerOutput(string(stdout), stderr)
					if isPatchFailure(log) {
						status = PatchFailure
					}
					fmt.Fprintln(os.Stderr, log)
					failures = append(failures, fmt.Errorf("%s: %s: %w", pkg.Name, status, updateErr))
				}
			}
		}
		abandoned := ""
		if pkg.Abandoned != nil && pkg.Abandoned != false {
			abandoned = fmt.Sprint(pkg.Abandoned)
		}
		fmt.Fprintf(&summary, "| %s | %s | %s | %s | %s |\n", packageLink(pkg.Name), cell(pkg.Version), releaseLink(pkg.Name, pkg.Latest), status, cell(abandoned))
	}
	failures = append(failures, publishReport(summary.String(), output))
	return errors.Join(failures...)
}

// trackedFiles holds the files whose change gives an update PR its content.
func trackedFiles() map[string]string {
	files := map[string]string{}
	for _, p := range []string{ComposerJsonFile, ComposerLockFile, "patches.lock.json"} {
		if b, e := os.ReadFile(p); e == nil {
			files[p] = string(b)
		}
	}
	return files
}
func isCore(name string) bool {
	return name == "drupal/core" || strings.HasPrefix(name, "drupal/core-")
}
func cell(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(value)
}
func appendReport(path, report string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(report)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}
