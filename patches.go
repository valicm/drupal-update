package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var skipPatched bool
var patchFailurePolicy = "fail"

// configuredPatches maps each patched package to its patch URLs from every
// source composer-patches reads: inline extra.patches, the 1.x
// extra.patches-file, the 2.x extra.composer-patches.patches-file (patches.json
// by default) and patches.lock.json, which also records dependency patches.
func configuredPatches(m manifest) (map[string][]string, error) {
	var extra struct {
		Patches         map[string]json.RawMessage `json:"patches"`
		PatchesFile     string                     `json:"patches-file"`
		ComposerPatches struct {
			PatchesFile string `json:"patches-file"`
		} `json:"composer-patches"`
	}
	if len(m["extra"]) > 0 {
		if e := json.Unmarshal(m["extra"], &extra); e != nil {
			return nil, e
		}
	}
	patches := map[string][]string{}
	add := func(definitions map[string]json.RawMessage) {
		for n, raw := range definitions {
			patches[n] = append(patches[n], patchURLs(raw)...)
		}
	}
	add(extra.Patches)
	// 1.x fails on a missing patches file; 2.x skips it silently.
	if extra.PatchesFile != "" {
		definitions, e := readPatchDefinitions(extra.PatchesFile)
		if e != nil {
			return nil, fmt.Errorf("read patch definitions: %w", e)
		}
		add(definitions)
	}
	version2File := extra.ComposerPatches.PatchesFile
	if version2File == "" {
		version2File = "patches.json"
	}
	for _, path := range []string{version2File, "patches.lock.json"} {
		if path == extra.PatchesFile {
			continue
		}
		definitions, e := readPatchDefinitions(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, fmt.Errorf("read patch definitions from %s: %w", path, e)
		}
		add(definitions)
	}
	return patches, nil
}
func readPatchDefinitions(path string) (map[string]json.RawMessage, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var file struct {
		Patches map[string]json.RawMessage `json:"patches"`
	}
	if e = json.Unmarshal(b, &file); e != nil {
		return nil, e
	}
	return file.Patches, nil
}

// patchURLs reads both styles: {"description": "url"} and [{"url": "…"}]. A
// definition in any other shape counts as one patch with an unknown URL ("").
func patchURLs(raw json.RawMessage) []string {
	switch strings.TrimSpace(string(raw)) {
	case "", "{}", "[]", "null":
		return nil
	}
	var urls []string
	var legacy map[string]string
	var expanded []struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &legacy) == nil {
		for _, u := range legacy {
			urls = append(urls, u)
		}
	} else if json.Unmarshal(raw, &expanded) == nil {
		for _, p := range expanded {
			urls = append(urls, p.URL)
		}
	}
	if len(urls) == 0 {
		return []string{""}
	}
	return urls
}
func hasPatches(name string, patches map[string][]string) bool {
	for n, urls := range patches {
		if len(urls) > 0 && (name == n || (isCore(name) && isCore(n))) {
			return true
		}
	}
	return false
}

// withoutSpace undoes Composer's wrapping of long exception lines, which can
// split a patch URL across lines.
func withoutSpace(s string) string {
	return strings.Join(strings.Fields(s), "")
}
func failedPatchTarget(log string, patches map[string][]string, args []string) string {
	text := withoutSpace(log)
	matches := map[string]bool{}
	for name, urls := range patches {
		for _, u := range urls {
			if u != "" && strings.Contains(text, withoutSpace(u)) {
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
	for _, n := range args {
		if target := targets[n]; target != "" {
			constraint, e := modeConstraint(roots[n], target)
			if e != nil {
				return "", fmt.Errorf("%s: %w", n, e)
			}
			if e = setConstraint(m, n, constraint); e != nil {
				return "", e
			}
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
