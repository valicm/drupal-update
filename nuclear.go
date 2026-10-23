package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

func nuclear(m manifest, report *strings.Builder) (result error) {
	backup, e := backupProject()
	if e != nil {
		return e
	}
	fmt.Fprintf(report, "Backup: `%s`\n\n", backup.dir)
	defer func() {
		if result != nil {
			restoreErr := backup.restore()
			result = errors.Join(result, restoreErr)
			if restoreErr == nil {
				report.WriteString("\nRecovery failed; restored manifests and lock files. Installed files may have changed.\n")
			} else {
				report.WriteString("\nRestoring manifests failed; restore from the backup directory.\n")
			}
		}
	}()
	if e := os.Remove(ComposerLockFile); e != nil && !os.IsNotExist(e) {
		return e
	}
	report.WriteString("- Removed composer.lock to resolve the full dependency graph.\n")
	relaxed := false
	for attempt := 1; attempt <= 8; attempt++ {
		fmt.Fprintf(report, "- Install attempt %d.\n", attempt)
		stdout, stderr, err := runComposer("install", "--no-interaction", "--no-progress")
		if err == nil {
			if _, e := lockedPackages(); e != nil {
				return fmt.Errorf("install did not create a valid lock: %w", e)
			}
			report.WriteString("\nComposer install completed. Review every constraint and patch change before using this result.\n")
			return nil
		}
		log := cleanComposerOutput(string(stdout), stderr)
		if isPatchFailure(log) {
			removed, e := removeFailedPatch(m, log)
			if e != nil {
				return e
			}
			if removed == "" {
				return fmt.Errorf("cannot identify a failing inline patch; external patch definitions require manual changes: %w\n%s", err, log)
			}
			fmt.Fprintf(report, "- Removed failing inline patch: %s.\n", cell(removed))
			// Regenerate both dependency and patch locks after changing patch definitions.
			for _, p := range []string{ComposerLockFile, "patches.lock.json"} {
				if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
			report.WriteString("- Removed dependency and patch locks for regeneration.\n")
			continue
		}
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) && exit.ExitCode() == 2 && !relaxed {
			roots := requirements(m)
			var names []string
			for n := range roots {
				if strings.HasPrefix(n, "drupal/") {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			for _, n := range names {
				if e := setConstraint(m, n, "*"); e != nil {
					return e
				}
				fmt.Fprintf(report, "- Relaxed %s: %s → *.\n", cell(n), cell(roots[n]))
			}
			if len(names) == 0 {
				return fmt.Errorf("no Drupal constraints to relax: %w\n%s", err, log)
			}
			relaxed = true
			if e := os.Remove(ComposerLockFile); e != nil && !os.IsNotExist(e) {
				return e
			}
			continue
		}
		return fmt.Errorf("no further automatic recovery for install failure: %w\n%s", err, log)
	}
	return fmt.Errorf("nuclear recovery exhausted eight install attempts")
}

// Remove one patch only when its URL occurs in the patch failure output.
// Support both legacy description=>URL maps and expanded inline patch arrays.
func removeFailedPatch(m manifest, log string) (string, error) {
	var extra map[string]json.RawMessage
	if len(m["extra"]) == 0 {
		return "", nil
	}
	if e := json.Unmarshal(m["extra"], &extra); e != nil {
		return "", e
	}
	var patches map[string]json.RawMessage
	if len(extra["patches"]) == 0 {
		return "", nil
	}
	if e := json.Unmarshal(extra["patches"], &patches); e != nil {
		return "", e
	}
	text := withoutSpace(log)
	failed := func(url string) bool { return url != "" && strings.Contains(text, withoutSpace(url)) }
	// Composer may print successful and failed patch URLs together. Refuse
	// removal when the output names multiple configured patches.
	matched := 0
	for _, raw := range patches {
		for _, url := range patchURLs(raw) {
			if failed(url) {
				matched++
			}
		}
	}
	if matched > 1 {
		return "", fmt.Errorf("patch failure output identifies multiple configured patches; refusing ambiguous removal")
	}
	var names []string
	for n := range patches {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var entries map[string]string
		if json.Unmarshal(patches[n], &entries) == nil {
			var descriptions []string
			for d := range entries {
				descriptions = append(descriptions, d)
			}
			sort.Strings(descriptions)
			for _, d := range descriptions {
				url := entries[d]
				if failed(url) {
					if e := removeManifestMember(func(_ int, key string) bool { return key == d }, "extra", "patches", n); e != nil {
						return "", e
					}
					delete(entries, d)
					b, _ := json.Marshal(entries)
					patches[n] = b
					return savePatches(m, extra, patches, n+": "+d+" ("+url+")")
				}
			}
		} else {
			var expanded []map[string]json.RawMessage
			if e := json.Unmarshal(patches[n], &expanded); e != nil {
				return "", fmt.Errorf("unsupported patch definition for %s: %w", n, e)
			}
			for i, p := range expanded {
				var url string
				_ = json.Unmarshal(p["url"], &url)
				if failed(url) {
					if e := removeManifestMember(func(j int, _ string) bool { return j == i }, "extra", "patches", n); e != nil {
						return "", e
					}
					expanded = append(expanded[:i], expanded[i+1:]...)
					b, _ := json.Marshal(expanded)
					patches[n] = b
					return savePatches(m, extra, patches, n+": "+url)
				}
			}
		}
	}
	return "", nil
}

// savePatches mirrors a removal already made in composer.json into m.
func savePatches(m manifest, extra map[string]json.RawMessage, patches map[string]json.RawMessage, removed string) (string, error) {
	b, e := json.Marshal(patches)
	if e != nil {
		return "", e
	}
	extra["patches"] = b
	b, e = json.Marshal(extra)
	if e != nil {
		return "", e
	}
	m["extra"] = b
	return removed, nil
}
