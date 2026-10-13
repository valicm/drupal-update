package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type manifest map[string]json.RawMessage

type snapshot struct {
	dir   string
	files map[string][]byte
}

func backupProject() (*snapshot, error) {
	s := &snapshot{files: map[string][]byte{}}
	for _, p := range []string{ComposerJsonFile, ComposerLockFile, "patches.lock.json"} {
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		s.files[p] = b
	}
	dir, e := os.MkdirTemp(".", ".drupal-update-backup-")
	if e != nil {
		return nil, e
	}
	s.dir = dir
	for p, b := range s.files {
		if e := os.WriteFile(filepath.Join(dir, p), b, 0600); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func (s *snapshot) restore() error {
	var errs []error
	for _, p := range []string{ComposerJsonFile, ComposerLockFile, "patches.lock.json"} {
		if b, ok := s.files[p]; ok {
			errs = append(errs, os.WriteFile(p, b, 0644))
		} else {
			e := os.Remove(p)
			if !os.IsNotExist(e) {
				errs = append(errs, e)
			}
		}
	}
	return errors.Join(errs...)
}
func readManifest() (manifest, error) {
	b, e := os.ReadFile(ComposerJsonFile)
	if e != nil {
		return nil, e
	}
	var m manifest
	e = json.Unmarshal(b, &m)
	return m, e
}
func writeManifest(m manifest) error {
	b, e := json.MarshalIndent(m, "", "    ")
	if e != nil {
		return e
	}
	return os.WriteFile(ComposerJsonFile, append(b, '\n'), 0644)
}
func requirements(m manifest) map[string]string {
	result := map[string]string{}
	for _, section := range []string{"require", "require-dev"} {
		var entries map[string]string
		_ = json.Unmarshal(m[section], &entries)
		for n, v := range entries {
			result[n] = v
		}
	}
	return result
}
func setConstraint(m manifest, name, value string) error {
	for _, section := range []string{"require", "require-dev"} {
		var entries map[string]string
		if len(m[section]) == 0 {
			continue
		}
		if e := json.Unmarshal(m[section], &entries); e != nil {
			return e
		}
		if _, ok := entries[name]; ok {
			entries[name] = value
			b, e := json.Marshal(entries)
			if e != nil {
				return e
			}
			m[section] = b
			return nil
		}
	}
	return fmt.Errorf("%s is not a direct requirement", name)
}

var stableVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?$`)

func version(v string) ([3]int, bool) {
	var result [3]int
	parts := stableVersion.FindStringSubmatch(v)
	if parts == nil {
		return result, false
	}
	for i := 0; i < 3; i++ {
		if parts[i+1] != "" {
			n, e := strconv.Atoi(parts[i+1])
			if e != nil {
				return result, false
			}
			result[i] = n
		}
	}
	return result, true
}
func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
func targetVersion(current string, versions []string, mode string) (string, error) {
	old, ok := version(current)
	prerelease := false
	if !ok && releaseVersion.MatchString(current) {
		old, ok = version(strings.SplitN(current, "-", 2)[0])
		prerelease = ok
	}
	if !ok {
		return "", fmt.Errorf("unsupported non-stable locked version %q", current)
	}
	best := old
	target := ""
	for _, v := range versions {
		n, ok := version(v)
		if !ok {
			continue
		}
		if mode == "minor" && n[0] != old[0] {
			continue
		}
		if mode == "major" && n[0] <= old[0] {
			continue
		}
		if newer(n, best) || (prerelease && n == best && target == "") {
			best = n
			target = strings.TrimPrefix(v, "v")
		}
	}
	return target, nil
}
func excludedPackage(name, core, exclude string) bool {
	if !activeSelection.includes(name) {
		return true
	}
	if isCore(name) && core == "false" {
		return true
	}
	for _, n := range strings.Split(exclude, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.Contains(n, "/") {
			n = "drupal/" + n
		}
		if n == name || (isCore(name) && isCore(n)) {
			return true
		}
	}
	return false
}
func lockedPackages() (map[string]string, error) {
	b, e := os.ReadFile(ComposerLockFile)
	if e != nil {
		return nil, e
	}
	var lock struct {
		Packages []Update `json:"packages"`
		Dev      []Update `json:"packages-dev"`
	}
	if e = json.Unmarshal(b, &lock); e != nil {
		return nil, e
	}
	result := map[string]string{}
	for _, p := range append(lock.Packages, lock.Dev...) {
		result[p.Name] = p.Version
	}
	return result, nil
}
func auditPackages() (map[string]bool, error) {
	b, stderr, commandErr := runComposer("audit", "--locked", "--format=json", "--no-interaction")
	var report map[string]json.RawMessage
	if e := json.Unmarshal(b, &report); e != nil {
		return nil, fmt.Errorf("audit failed: %v: %s: %w", commandErr, stderr, e)
	}
	raw, ok := report["advisories"]
	if !ok || string(raw) == "null" {
		return nil, fmt.Errorf("audit returned no advisory data: %v: %s", commandErr, stderr)
	}
	advisories := map[string]json.RawMessage{}
	if strings.TrimSpace(string(raw)) != "[]" {
		if e := json.Unmarshal(raw, &advisories); e != nil {
			return nil, e
		}
	}
	if unreachable := report["unreachable-repositories"]; len(unreachable) > 0 && string(unreachable) != "[]" && string(unreachable) != "{}" {
		return nil, fmt.Errorf("audit repositories unreachable: %s", unreachable)
	}
	if commandErr != nil {
		var exit interface{ ExitCode() int }
		if !errors.As(commandErr, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 3 {
			return nil, fmt.Errorf("audit unsuccessful: %w: %s", commandErr, stderr)
		}
	}
	affected := map[string]bool{}
	for n, raw := range advisories {
		if string(raw) != "[]" && string(raw) != "{}" && string(raw) != "null" {
			affected[n] = true
		}
	}
	return affected, nil
}
func modeReport(report, output string) error {
	fmt.Print(report)
	if output != "" {
		if e := os.WriteFile(output, []byte(report), 0644); e != nil {
			return e
		}
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if e := appendReport(os.Getenv("GITHUB_STEP_SUMMARY"), report); e != nil {
			return e
		}
		delimiter := "DRUPAL_UPDATE_REPORT_END"
		for strings.Contains(report, delimiter) {
			delimiter += "_"
		}
		return appendReport(os.Getenv("GITHUB_ENV"), "DRUPAL_UPDATES_TABLE<<"+delimiter+"\n"+report+delimiter+"\n")
	}
	return nil
}
func runMode(mode, core, exclude, output string) (result error) {
	if mode == "nuclear" && (core == "false" || strings.TrimSpace(exclude) != "") {
		return fmt.Errorf("nuclear resolves the entire dependency graph; core=false and exclusions cannot be enforced")
	}
	m, e := readManifest()
	if e != nil {
		return e
	}
	locked, e := lockedPackages()
	if e != nil {
		return e
	}
	var report strings.Builder
	fmt.Fprintf(&report, "### Drupal %s upgrade\n\n", mode)
	defer func() {
		if result != nil {
			report.WriteString(failureMarkdown(result.Error()))
		}
		result = errors.Join(result, modeReport(report.String(), output))
	}()
	if mode == "nuclear" {
		return nuclear(m, &report)
	}
	affected := map[string]bool{}
	if mode == "security" {
		affected, e = auditPackages()
		if e != nil {
			return e
		}
	}
	patches, e := configuredPatches(m)
	if e != nil {
		return e
	}
	roots := requirements(m)
	var names []string
	for n := range locked {
		if strings.HasPrefix(n, "drupal/") && !excludedPackage(n, core, exclude) {
			if skipPatched && hasPatches(n, patches) {
				fmt.Fprintf(&report, "- Skipped %s: configured patches retained.\n", packageLink(n))
				continue
			}
			if mode == "security" {
				if affected[n] {
					names = append(names, n)
				}
			} else if _, ok := roots[n]; ok {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	targets := map[string]string{}
	var args []string
	for _, n := range names {
		if mode == "security" {
			args = append(args, n)
			continue
		}
		if !releaseVersion.MatchString(locked[n]) || developmentConstraint(roots[n]) {
			fmt.Fprintf(&report, "- Skipped %s: development or unsupported locked version %s; requirement retained.\n", packageLink(n), cell(locked[n]))
			continue
		}
		b, stderr, e := runComposer("show", n, "--all", "--format=json", "--no-interaction")
		if e != nil {
			return fmt.Errorf("list %s versions: %w: %s", n, e, stderr)
		}
		var available struct {
			Versions []string `json:"versions"`
		}
		if e = json.Unmarshal(b, &available); e != nil {
			return e
		}
		target, e := targetVersion(locked[n], available.Versions, mode)
		if e != nil {
			return fmt.Errorf("%s: %w", n, e)
		}
		if target != "" {
			targets[n] = target
		}
	}
	// Align only the Drupal core release packages already present, across both sections.
	coreTarget := ""
	for _, n := range []string{"drupal/core-recommended", "drupal/core"} {
		if targets[n] != "" {
			coreTarget = targets[n]
			break
		}
	}
	if coreTarget != "" {
		for _, n := range coreReleasePackages {
			if _, ok := roots[n]; ok {
				targets[n] = coreTarget
			}
		}
	}
	for n := range targets {
		args = append(args, n)
	}
	sort.Strings(args)
	if len(args) == 0 {
		report.WriteString("No eligible upgrades found.\n")
		return nil
	}
	backup, e := backupProject()
	if e != nil {
		return e
	}
	fmt.Fprintf(&report, "Backup: `%s`\n\n", backup.dir)
	defer func() {
		if result != nil {
			restoreErr := backup.restore()
			result = errors.Join(result, restoreErr)
			if restoreErr == nil {
				report.WriteString("\nRestored Composer manifests and lock files. Installed files may have changed; run composer install after reviewing the failure.\n")
			} else {
				report.WriteString("\nManifest restore failed; use the backup directory.\n")
			}
		}
	}()
	for _, n := range args {
		if targets[n] != "" {
			fmt.Fprintf(&report, "- Selected %s: %s → %s\n", packageLink(n), cell(locked[n]), releaseLink(n, targets[n]))
		} else {
			fmt.Fprintf(&report, "- Selected security update %s (%s)\n", packageLink(n), cell(locked[n]))
		}
	}
	for len(args) > 0 {
		log, updateErr := applyUpdateTargets(m, roots, args, targets)
		if updateErr == nil {
			break
		}
		failed := ""
		if patchFailurePolicy == "skip" && isPatchFailure(log) {
			failed = failedPatchTarget(log, patches, args)
		}
		if failed == "" {
			return fmt.Errorf("update failed: %w\n%s", updateErr, log)
		}
		if e := backup.restore(); e != nil {
			return e
		}
		m, e = readManifest()
		if e != nil {
			return e
		}
		fmt.Fprintf(&report, "- Skipped %s after a patch failure; restored manifests and locks before retrying.\n", packageLink(failed))
		remaining := []string{}
		for _, n := range args {
			if n != failed && !(isCore(n) && isCore(failed)) {
				remaining = append(remaining, n)
			}
		}
		args = remaining
	}
	if len(args) == 0 {
		report.WriteString("\nAll selected upgrades were skipped after patch failures. No manifests or locks were changed. Installed files may need reconciliation with composer install.\n")
		return nil
	}
	if mode == "security" {
		remaining, e := auditPackages()
		if e != nil {
			return e
		}
		for _, n := range args {
			if remaining[n] {
				return fmt.Errorf("security advisory remains for %s under the current constraints", n)
			}
		}
	}
	installed, e := lockedPackages()
	if e != nil {
		return e
	}
	for _, n := range args {
		if targets[n] != "" {
			got, ok := version(installed[n])
			want, _ := version(targets[n])
			if !ok || got != want {
				return fmt.Errorf("%s did not resolve to %s", n, targets[n])
			}
		}
		fmt.Fprintf(&report, "- Resolved %s: %s\n", packageLink(n), releaseLink(n, installed[n]))
	}
	report.WriteString("\nUpgrade completed.\n")
	return nil
}
