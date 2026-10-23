package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func wrapperSource(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("drupal-update.sh")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func writeWrapperFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.WriteFile(path, b, 0755); e != nil {
		t.Fatal(e)
	}
}

const wrapperFakeBinary = "#!/bin/bash\nprintf '%s\\n' \"$PWD\" \"$@\" > \"$WRAPPER_CAPTURE\"\nprintf 'binary stdout\\n'\nprintf 'binary stderr\\n' >&2\nexit 42\n"

func TestBashWrapperForwardsArgumentsAndStatus(t *testing.T) {
	source := wrapperSource(t)
	for _, piped := range []bool{false, true} {
		t.Run(fmt.Sprint(piped), func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "binary with spaces")
			writeWrapperFile(t, binary, []byte(wrapperFakeBinary))
			script := filepath.Join(dir, "drupal-update.sh")
			writeWrapperFile(t, script, source)
			args := []string{"-t", "all", "-c", "false", "-e", "token,redirect", "-o", "report with spaces.md"}
			command := append([]string{script}, args...)
			if piped {
				command = append([]string{"-s", "--"}, args...)
			}
			cmd := exec.Command("bash", command...)
			cmd.Dir = dir
			capture := filepath.Join(dir, "capture")
			cmd.Env = append(os.Environ(), "DRUPAL_UPDATE_BINARY="+binary, "WRAPPER_CAPTURE="+capture)
			if piped {
				cmd.Stdin = bytes.NewReader(source)
			}
			output, e := cmd.CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 42 {
				t.Fatalf("status: %v %s", e, output)
			}
			b, e := os.ReadFile(capture)
			if e != nil {
				t.Fatal(e)
			}
			if string(b) != dir+"\n"+strings.Join(args, "\n")+"\n" {
				t.Fatal(string(b))
			}
			if !strings.Contains(string(output), "binary stdout") || !strings.Contains(string(output), "binary stderr") {
				t.Fatal(string(output))
			}
		})
	}
}
func TestBashWrapperAdjacentBinary(t *testing.T) {
	dir := t.TempDir()
	writeWrapperFile(t, filepath.Join(dir, "drupal-update.sh"), wrapperSource(t))
	writeWrapperFile(t, filepath.Join(dir, "drupal-update"), []byte(wrapperFakeBinary))
	cmd := exec.Command("bash", filepath.Join(dir, "drupal-update.sh"), "--help")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DRUPAL_UPDATE_BINARY=", "DRUPAL_UPDATE_VERSION=not-a-version", "WRAPPER_CAPTURE="+filepath.Join(dir, "capture"))
	if e := cmd.Run(); e == nil || e.(*exec.ExitError).ExitCode() != 42 {
		t.Fatal(e)
	}
}
func TestBashWrapperVerifiedDownloadAndCleanup(t *testing.T) {
	if _, e := exec.LookPath("sha256sum"); e != nil {
		t.Skip("sha256sum unavailable")
	}
	if _, e := exec.LookPath("curl"); e != nil {
		t.Skip("curl unavailable")
	}
	source := wrapperSource(t)
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			if e := os.Mkdir(tools, 0755); e != nil {
				t.Fatal(e)
			}
			script := filepath.Join(dir, "drupal-update.sh")
			writeWrapperFile(t, script, source)
			// An explicit release wins over building a source checkout beside the wrapper.
			writeWrapperFile(t, filepath.Join(dir, "go.mod"), []byte("module test\n"))
			writeWrapperFile(t, filepath.Join(dir, "main.go"), []byte("package main\n"))
			built := filepath.Join(dir, "built")
			writeWrapperFile(t, filepath.Join(tools, "go"), []byte("#!/bin/bash\ntouch \""+built+"\"\nexit 1\n"))
			system, e := exec.Command("uname", "-s").Output()
			if e != nil {
				t.Fatal(e)
			}
			arch, e := exec.Command("uname", "-m").Output()
			if e != nil {
				t.Fatal(e)
			}
			osName := map[string]string{"Linux": "linux", "Darwin": "darwin"}[strings.TrimSpace(string(system))]
			archName := map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[strings.TrimSpace(string(arch))]
			if osName == "" || archName == "" {
				t.Skip("POSIX release target unavailable")
			}
			name := "drupal-update_v5.0.0_" + osName + "_" + archName + ".tar.gz"
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if e = tw.WriteHeader(&tar.Header{Name: "drupal-update", Mode: 0755, Size: int64(len(wrapperFakeBinary))}); e != nil {
				t.Fatal(e)
			}
			_, _ = tw.Write([]byte(wrapperFakeBinary))
			if e = tw.Close(); e != nil {
				t.Fatal(e)
			}
			if e = gz.Close(); e != nil {
				t.Fatal(e)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
			if corrupt {
				digest = strings.Repeat("0", 64)
			}
			writeWrapperFile(t, filepath.Join(dir, name), archive.Bytes())
			writeWrapperFile(t, filepath.Join(dir, "SHA256SUMS"), []byte(digest+"  "+name+"\n"))
			curl := `#!/bin/bash
set -eu
url=$2
out=$4
if [[ "$2" == -o ]]; then
  printf 'https://github.com/valicm/drupal-update/releases/tag/v5.0.0'
else
  printf '%s\n' "$(dirname "$out")" > "$TEST_TEMP_RECORD"
  cp "$TEST_RELEASE_DIR/${url##*/}" "$out"
fi
`
			writeWrapperFile(t, filepath.Join(tools, "curl"), []byte(curl))
			capture := filepath.Join(dir, "capture")
			record := filepath.Join(dir, "temp-record")
			cmd := exec.Command("bash", script, "-t", "minor")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+tools+":/usr/bin:/bin", "DRUPAL_UPDATE_BINARY=", "DRUPAL_UPDATE_VERSION=latest", "WRAPPER_CAPTURE="+capture, "TEST_RELEASE_DIR="+dir, "TEST_TEMP_RECORD="+record)
			output, e := cmd.CombinedOutput()
			if e == nil {
				t.Fatal("expected fake binary status or checksum failure")
			}
			if !corrupt && e.(*exec.ExitError).ExitCode() != 42 {
				t.Fatalf("%v %s", e, output)
			}
			_, captureErr := os.Stat(capture)
			if corrupt && !os.IsNotExist(captureErr) {
				t.Fatal("executed an unverified binary")
			}
			if !corrupt && captureErr != nil {
				t.Fatal(captureErr)
			}
			if _, e = os.Stat(built); !os.IsNotExist(e) {
				t.Fatal("built the source checkout instead of the requested release")
			}
			temp, e := os.ReadFile(record)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(strings.TrimSpace(string(temp))); !os.IsNotExist(e) {
				t.Fatal("temporary download was not cleaned")
			}
		})
	}
}

func TestBashWrapperSourceBuildKeepsCallerDirectory(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	caller := filepath.Join(dir, "caller")
	tools := filepath.Join(dir, "tools")
	for _, path := range []string{sourceDir, caller, tools} {
		if e := os.Mkdir(path, 0755); e != nil {
			t.Fatal(e)
		}
	}
	writeWrapperFile(t, filepath.Join(sourceDir, "drupal-update.sh"), wrapperSource(t))
	writeWrapperFile(t, filepath.Join(sourceDir, "go.mod"), []byte("module test\n"))
	writeWrapperFile(t, filepath.Join(sourceDir, "main.go"), []byte("package main\n"))
	fakeBinary := filepath.Join(dir, "fake-binary")
	writeWrapperFile(t, fakeBinary, []byte(wrapperFakeBinary))
	writeWrapperFile(t, filepath.Join(tools, "go"), []byte("#!/bin/bash\nset -eu\nprintf '%s' \"$PWD\" > \"$BUILD_CAPTURE\"\ncp \"$TEST_FAKE_BINARY\" \"$4\"\nchmod +x \"$4\"\n"))
	capture := filepath.Join(dir, "capture")
	buildCapture := filepath.Join(dir, "build-capture")
	cmd := exec.Command("bash", filepath.Join(sourceDir, "drupal-update.sh"), "-o", "report.md")
	cmd.Dir = caller
	cmd.Env = append(os.Environ(), "PATH="+tools+":/usr/bin:/bin", "DRUPAL_UPDATE_BINARY=", "DRUPAL_UPDATE_VERSION=", "WRAPPER_CAPTURE="+capture, "BUILD_CAPTURE="+buildCapture, "TEST_FAKE_BINARY="+fakeBinary)
	b, e := cmd.CombinedOutput()
	if e == nil || e.(*exec.ExitError).ExitCode() != 42 {
		t.Fatalf("%v %s", e, b)
	}
	buildDir, e := os.ReadFile(buildCapture)
	if e != nil || string(buildDir) != sourceDir {
		t.Fatal("build did not run in source checkout")
	}
	args, e := os.ReadFile(capture)
	if e != nil || string(args) != caller+"\n-o\nreport.md\n" {
		t.Fatal("upgrade did not run in caller's directory")
	}
}
