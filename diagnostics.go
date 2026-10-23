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
	for line := range strings.SplitSeq(ansiEscape.ReplaceAllString(stdout+"\n"+stderr, ""), "\n") {
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

// Patch failure messages from composer-patches 1.x and 2.x.
var patchFailureMessages = []string{"could not apply patch", "cannot apply patch", "could not be applied", "no available patcher was able to apply patch"}

func isPatchFailure(log string) bool {
	text := withoutSpace(strings.ToLower(log))
	for _, message := range patchFailureMessages {
		if strings.Contains(text, withoutSpace(message)) {
			return true
		}
	}
	return false
}
