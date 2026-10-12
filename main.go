package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/urfave/cli/v2"
	"os"
	"os/exec"
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
	Name      string      `json:"name"`
	Version   string      `json:"version"`
	Latest    string      `json:"latest"`
	Status    string      `json:"latest-status"`
	Homepage  string      `json:"homepage"`
	Abandoned interface{} `json:"abandoned"`
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
			previousCommand, previousSelection := composerCommand, activeSelection
			composerCommand, activeSelection = launcher, selection
			defer func() { composerCommand, activeSelection = previousCommand, previousSelection }()
			// Validate requirements
			if err := validateRequirements(); err != nil {
				return err
			}

			// Perform update
			if err := updateProjects(updateType, core, exclude, output); err != nil {
				return err
			}

			return nil
		},
	}
}

func main() {
	err := newApp().Run(os.Args)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

func validateOptions(updateType, core, output string) error {
	if updateType != SemverSafeUpdate && updateType != UpdateAll && updateType != "security" && updateType != "minor" && updateType != "major" && updateType != "nuclear" {
		return fmt.Errorf("invalid update type %q", updateType)
	}

	if core != "true" && core != "false" {
		return fmt.Errorf("error: Core flag must be either true or false. Default if empty is false")
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
		fmt.Printf("%s is available at %s\n", binary, path)
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
		skip := excludedPackage(pkg.Name, core, exclude) || (updateType != UpdateAll && pkg.Status != SemverSafeUpdate)
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
				fmt.Printf("Updating %s from %s towards %s\n", pkg.Name, pkg.Version, pkg.Latest)
				stdout, stderr, updateErr := runComposer(args...)
				if updateErr == nil {
					status = Success
				} else {
					status = GenericError
					var exit *exec.ExitError
					if errors.As(updateErr, &exit) && exit.ExitCode() == 2 {
						status = DependencyError
					}
					log := string(stdout) + stderr
					if strings.Contains(strings.ToLower(log), "could not apply patch") || strings.Contains(strings.ToLower(log), "cannot apply patch") {
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
	report := summary.String()
	fmt.Print(report)
	if output != "" {
		if err := os.WriteFile(output, []byte(report), 0644); err != nil {
			return err
		}
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if err := appendReport(os.Getenv("GITHUB_STEP_SUMMARY"), report); err != nil {
			return err
		}
		// Unique delimiter avoids interpreting report content as environment commands.
		delimiter := "DRUPAL_UPDATE_REPORT_END"
		for strings.Contains(report, delimiter) {
			delimiter += "_"
		}
		if err := appendReport(os.Getenv("GITHUB_ENV"), "DRUPAL_UPDATES_TABLE<<"+delimiter+"\n"+report+delimiter+"\n"); err != nil {
			return err
		}
	}
	return errors.Join(failures...)
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
