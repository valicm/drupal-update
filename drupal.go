package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var publishReport = modeReport

var runDrush = func(command []string, args ...string) ([]byte, error) {
	argv := append(append([]string{}, command[1:]...), args...)
	return exec.Command(command[0], argv...).CombinedOutput()
}

type drupalLifecycle struct {
	install, updateDB, export bool
	command                   string
}

// Keep database mutations outside Composer's file rollback transaction.
func (d drupalLifecycle) run(output string, upgrade func() error) (result error) {
	if !d.install && !d.updateDB && !d.export {
		return upgrade()
	}
	if d.export && !d.updateDB {
		return fmt.Errorf("export-config requires updatedb so configuration is exported after database updates")
	}
	command, err := splitCommand(d.command)
	if err != nil {
		return fmt.Errorf("drush-command: %w", err)
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		return fmt.Errorf("drush-command: %w; install locked Composer dependencies before running the updater", err)
	}
	var report strings.Builder
	previous := publishReport
	publishReport = func(text, _ string) error { report.WriteString(text); return nil }
	defer func() {
		publishReport = previous
		result = errors.Join(result, previous(report.String(), output))
	}()
	step := func(args ...string) error {
		name := strings.Join(args, " ")
		fmt.Fprintf(os.Stderr, "Running Drush %s\n", name)
		log, err := runDrush(command, args...)
		if err != nil {
			failure := fmt.Errorf("Drush %s failed: %w\n%s", name, err, cleanComposerOutput(string(log), ""))
			report.WriteString(failureMarkdown(failure.Error()))
			report.WriteString("\nDatabase, installed files and configuration may have changed. No database rollback is performed; review the site before retrying.\n")
			return failure
		}
		fmt.Fprint(os.Stderr, string(log))
		fmt.Fprintf(&report, "\nDrush `%s` completed.\n", name)
		return nil
	}
	if d.install {
		if err := step("site:install", "--existing-config", "--yes"); err != nil {
			return err
		}
	}
	if err := upgrade(); err != nil {
		report.WriteString("\nPost-upgrade Drush commands skipped because the Composer upgrade failed.\n")
		return err
	}
	if d.updateDB {
		if err := step("updatedb", "--yes"); err != nil {
			return err
		}
	}
	if d.export {
		return step("config:export", "--yes")
	}
	return nil
}
