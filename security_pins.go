package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

var securityUpdatePinned bool

func exactRequirement(value string) bool {
	return pinnedConstraint.MatchString(strings.TrimSpace(strings.SplitN(value, "@", 2)[0]))
}

func latestSecurityPatch(name, current string) (string, error) {
	data, stderr, e := runComposer("show", name, "--all", "--format=json", "--no-interaction")
	if e != nil {
		return "", fmt.Errorf("list patch releases for %s: %w: %s", name, e, stderr)
	}
	var available struct {
		Versions []string `json:"versions"`
	}
	if e = json.Unmarshal(data, &available); e != nil {
		return "", e
	}
	target, e := targetVersion(current, available.Versions, "patch")
	if e != nil {
		return "", fmt.Errorf("%s: %w", name, e)
	}
	return target, nil
}

// Rewrite only selected direct exact pins. Core release pins share one target;
// ranges, unrelated requirements, and development requirements keep their sections.
func securityPinnedTargets(names []string, roots, locked map[string]string) (map[string]string, error) {
	result := map[string]string{}
	selected := map[string]bool{}
	for _, n := range names {
		selected[n] = true
	}
	var corePins []string
	for _, n := range coreReleasePackages {
		if selected[n] && exactRequirement(roots[n]) {
			corePins = append(corePins, n)
		}
	}
	if len(corePins) > 0 {
		anchor := corePins[0]
		for _, n := range []string{"drupal/core", "drupal/core-recommended"} {
			if selected[n] {
				anchor = n
				break
			}
		}
		target, e := latestSecurityPatch(anchor, locked[anchor])
		if e != nil {
			return nil, e
		}
		if target != "" {
			chosen, _ := version(target)
			for _, n := range corePins {
				current, ok := version(locked[n])
				if !ok || current[0] != chosen[0] || current[1] != chosen[1] {
					return nil, fmt.Errorf("cannot coordinate %s across minor versions in security pin updates", n)
				}
				result[n] = target
			}
		}
	}
	for _, n := range names {
		if !exactRequirement(roots[n]) {
			continue
		}
		isRelease := slices.Contains(coreReleasePackages, n)
		if isRelease {
			continue
		}
		target, e := latestSecurityPatch(n, locked[n])
		if e != nil {
			return nil, e
		}
		if target != "" {
			result[n] = target
		}
	}
	return result, nil
}
