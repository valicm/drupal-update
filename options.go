package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var composerCommand = []string{"composer"}
var activeSelection = selection{scope: "all"}

type selection struct {
	scope    string
	packages map[string]bool
}

var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)

func parseSelection(scope, packages string) (selection, error) {
	result := selection{scope: scope, packages: map[string]bool{}}
	if scope != "all" && scope != "core" && scope != "contrib" {
		return result, fmt.Errorf("invalid scope %q: use all, core or contrib", scope)
	}
	for _, n := range strings.Split(packages, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.Contains(n, "/") {
			n = "drupal/" + n
		}
		if !packageName.MatchString(n) || !strings.HasPrefix(n, "drupal/") {
			return result, fmt.Errorf("invalid Drupal package %q", n)
		}
		if scope == "core" && !isCore(n) || scope == "contrib" && isCore(n) {
			return result, fmt.Errorf("package %s conflicts with scope %s", n, scope)
		}
		result.packages[n] = true
	}
	return result, nil
}
func (s selection) includes(name string) bool {
	if s.scope == "core" && !isCore(name) || s.scope == "contrib" && isCore(name) {
		return false
	}
	if len(s.packages) == 0 {
		return true
	}
	if s.packages[name] {
		return true
	}
	if isCore(name) {
		for n := range s.packages {
			if isCore(n) {
				return true
			}
		}
	}
	return false
}

// Parse quoting for executable arguments; never invoke a shell or expand variables.
func splitCommand(input string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, c := range input {
		if escaped {
			word.WriteRune(c)
			escaped = false
			started = true
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if unicode.IsSpace(c) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(c)
		started = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quoting in composer-command")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, fmt.Errorf("composer-command must contain an executable")
	}
	return args, nil
}

var simpleConstraint = regexp.MustCompile(`^[~^]v?\d+(?:\.\d+){0,2}$`)
var pinnedConstraint = regexp.MustCompile(`^v?\d+(?:\.\d+){0,2}$`)
var wildcardConstraint = regexp.MustCompile(`^v?\d+(?:\.\d+)?\.[*xX]$`)

func upgradeConstraint(previous, target string) (string, error) {
	if _, ok := version(target); !ok {
		return "", fmt.Errorf("cannot rewrite requirement to non-stable target %q", target)
	}
	previous = strings.TrimSpace(previous)
	suffix := ""
	if i := strings.LastIndex(previous, "@"); i >= 0 {
		suffix = previous[i:]
		previous = previous[:i]
	}
	if simpleConstraint.MatchString(previous) {
		return previous[:1] + target + suffix, nil
	}
	if pinnedConstraint.MatchString(previous) {
		return target + suffix, nil
	}
	if previous == "*" {
		return previous + suffix, nil
	}
	if wildcardConstraint.MatchString(previous) {
		parts := strings.Split(previous, ".")
		newParts := strings.Split(target, ".")
		return strings.Join(newParts[:len(parts)-1], ".") + "." + parts[len(parts)-1] + suffix, nil
	}
	return "", fmt.Errorf("cannot automatically rewrite constraint %q; use a simple caret, tilde, wildcard or exact constraint", previous)
}
func projectURL(name string) string {
	slug := strings.TrimPrefix(name, "drupal/")
	if isCore(name) {
		slug = "drupal"
	}
	return "https://www.drupal.org/project/" + url.PathEscape(slug)
}
func markdownLabel(value string) string {
	return strings.NewReplacer("[", "\\[", "]", "\\]", "|", "\\|", "\n", " ", "\r", " ").Replace(value)
}
func packageLink(name string) string {
	return "[" + markdownLabel(name) + "](" + projectURL(name) + ")"
}

var releaseVersion = regexp.MustCompile(`(?i)^v?\d+\.\d+(?:\.\d+)?(?:-(?:alpha|beta|rc)\d*)?$`)

func releaseLink(name, release string) string {
	if !releaseVersion.MatchString(release) {
		return cell(release)
	}
	return "[" + markdownLabel(release) + "](" + projectURL(name) + "/releases/" + url.PathEscape(release) + ")"
}

// Compound alternatives remain unchanged; the temporary exact update target
// lets Composer enforce whether the selected version is allowed.
func modeConstraint(previous, target string) (string, error) {
	if strings.Contains(previous, "||") {
		for _, part := range strings.Split(previous, "||") {
			if _, e := upgradeConstraint(strings.TrimSpace(part), target); e != nil {
				return "", e
			}
		}
		return previous, nil
	}
	return upgradeConstraint(previous, target)
}

var coreReleasePackages = []string{"drupal/core", "drupal/core-recommended", "drupal/core-composer-scaffold", "drupal/core-project-message", "drupal/core-dev", "drupal/core-recipe-unpack"}

func developmentConstraint(constraint string) bool {
	base := strings.ToLower(strings.SplitN(constraint, "@", 2)[0])
	return strings.Contains(base, "dev-") || strings.Contains(base, "-dev")
}
