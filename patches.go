package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var skipPatched bool
var patchFailurePolicy = "fail"

func configuredPatches(m manifest) (map[string]json.RawMessage, error) {
	var extra map[string]json.RawMessage
	if len(m["extra"]) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	if e := json.Unmarshal(m["extra"], &extra); e != nil {
		return nil, e
	}
	var patches map[string]json.RawMessage
	if raw := extra["patches"]; len(raw) > 0 && string(raw) != "null" {
		if e := json.Unmarshal(raw, &patches); e != nil {
			return nil, e
		}
	}
	if patches == nil {
		patches = map[string]json.RawMessage{}
	}
	if raw := extra["patches-file"]; len(raw) > 0 {
		var path string
		if e := json.Unmarshal(raw, &path); e != nil {
			return nil, e
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, fmt.Errorf("read patch definitions: %w", e)
		}
		var external struct {
			Patches map[string]json.RawMessage `json:"patches"`
		}
		if e = json.Unmarshal(b, &external); e != nil {
			return nil, e
		}
		for n, v := range external.Patches {
			patches[n] = v
		}
	}
	return patches, nil
}
func hasPatches(name string, patches map[string]json.RawMessage) bool {
	for n, raw := range patches {
		if string(raw) == "{}" || string(raw) == "[]" || string(raw) == "null" {
			continue
		}
		if name == n || (isCore(name) && isCore(n)) {
			return true
		}
	}
	return false
}
func failedPatchTarget(log string, patches map[string]json.RawMessage, args []string) string {
	matches := map[string]bool{}
	for name, raw := range patches {
		var legacy map[string]string
		var urls []string
		if json.Unmarshal(raw, &legacy) == nil {
			for _, u := range legacy {
				urls = append(urls, u)
			}
		} else {
			var expanded []struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(raw, &expanded) == nil {
				for _, p := range expanded {
					urls = append(urls, p.URL)
				}
			}
		}
		for _, u := range urls {
			if u != "" && strings.Contains(log, u) {
				matches[name] = true
			}
		}
	}
	if len(matches) != 1 {
		return ""
	}
	for name := range matches {
		for _, arg := range args {
			if arg == name || (isCore(arg) && isCore(name)) {
				return name
			}
		}
	}
	return ""
}
func applyUpdateTargets(m manifest, roots map[string]string, args []string, targets map[string]string) (string, error) {
	changed := false
	for _, n := range args {
		if target := targets[n]; target != "" {
			constraint, e := modeConstraint(roots[n], target)
			if e != nil {
				return "", fmt.Errorf("%s: %w", n, e)
			}
			if e = setConstraint(m, n, constraint); e != nil {
				return "", e
			}
			changed = true
		}
	}
	if changed {
		if e := writeManifest(m); e != nil {
			return "", e
		}
	}
	command := []string{"update"}
	for _, n := range args {
		if targets[n] != "" {
			command = append(command, n+":"+targets[n])
		} else {
			command = append(command, n)
		}
	}
	command = append(command, "--with-all-dependencies", "--minimal-changes", "--no-interaction", "--no-progress")
	stdout, stderr, e := runComposer(command...)
	return cleanComposerOutput(string(stdout), stderr), e
}
