package main

import (
	"os"
	"strings"
	"testing"
)

// USAGE.md's option reference must list every Action input and output.
func TestUsageListsActionInputsAndOutputs(t *testing.T) {
	action, e := os.ReadFile("action.yml")
	if e != nil {
		t.Fatal(e)
	}
	usage, e := os.ReadFile("USAGE.md")
	if e != nil {
		t.Fatal(e)
	}
	section := ""
	var names []string
	for line := range strings.SplitSeq(string(action), "\n") {
		if !strings.HasPrefix(line, " ") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		if (section == "inputs" || section == "outputs") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			names = append(names, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	if len(names) < 16 {
		t.Fatalf("parsed only %v", names)
	}
	for _, n := range names {
		if !strings.Contains(string(usage), "| `"+n+"` |") {
			t.Errorf("USAGE.md option reference does not list %s", n)
		}
	}
}
