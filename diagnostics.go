package main

import (
	"fmt"
	"regexp"
	"strings"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var workflowAnnotation = regexp.MustCompile(`^::(?:error|warning|notice)(?: [^:]*)?::(.*)$`)

func cleanComposerOutput(stdout, stderr string) string {
	var plain, annotations []string
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(stdout+"\n"+stderr, ""), "\n") {
		trimmed := strings.TrimSpace(line)
		if match := workflowAnnotation.FindStringSubmatch(trimmed); match != nil {
			annotations = append(annotations, strings.NewReplacer("%0A", "\n", "%0D", "\r", "%25", "%").Replace(match[1]))
			continue
		}
		if strings.HasPrefix(trimmed, "Loading composer repositories") || trimmed == "Updating dependencies" {
			continue
		}
		plain = append(plain, line)
	}
	result := strings.TrimSpace(strings.Join(plain, "\n"))
	for _, annotation := range annotations {
		normalized := strings.Join(strings.Fields(annotation), " ")
		if normalized != "" && !strings.Contains(strings.Join(strings.Fields(result), " "), normalized) {
			result = strings.TrimSpace(result + "\n" + annotation)
		}
	}
	return result
}
func failureMarkdown(message string) string {
	message = cleanComposerOutput(message, "")
	fence := "```"
	for strings.Contains(message, fence) {
		fence += "`"
	}
	return fmt.Sprintf("\n### Upgrade failed\n\n%stext\n%s\n%s\n", fence, message, fence)
}
func isPatchFailure(log string) bool {
	lower := strings.ToLower(log)
	return strings.Contains(lower, "could not apply patch") || strings.Contains(lower, "cannot apply patch") || strings.Contains(lower, "could not be applied")
}
