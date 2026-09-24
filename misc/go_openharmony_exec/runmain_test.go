// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRunMainPushesSourceTree drives the whole wrapper against a fake hdc and
// checks the three things a test needs beyond "the binary ran": the package's
// working directory is mirrored onto the device, the binary is started from that
// mirror, and nothing the wrapper says on its own account reaches the streams it
// is proxying.
//
// Losing the first two is silent. The wrapper still reports the test's own exit
// status, so packages whose tests read files next to their sources -- os looks
// for stat_linux.go in the working directory, io/fs's TestGlob walks it,
// text/template opens testdata/ -- just fail on device for reasons that have
// nothing to do with the port.
//
// The fake hdc never echoes the sync marker, so this is also the acceptance for
// the degraded case: a GOROOT sync that fails -- an unwritable GOROOT, a device
// that refuses the push -- must still leave the package mirror in place.
// Returning early there would trade a skip for an ENOENT, which is the
// misdiagnosis the mirror exists to prevent.
func TestRunMainPushesSourceTree(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hdc.log")
	script := "#!/bin/sh\n" +
		`echo "$@" >> "` + logPath + `"` + "\n" +
		// The wrapper recovers the remote exit status from this sentinel.
		`case "$*" in *shell*) echo "` + exitStr + `0";; esac` + "\n"
	fakeHDC := filepath.Join(t.TempDir(), "hdc")
	if err := os.WriteFile(fakeHDC, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// `go test` runs the wrapper with its working directory set to the package
	// directory; a temp dir with a test source in it stands in for one.
	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "pkg_test.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "pkg.test")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OHOS_HDC", fakeHDC)
	t.Setenv("OHOS_TARGET", "test-target")
	t.Setenv("TMPDIR", t.TempDir()) // lock() writes here; keep it in the test's tree

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	args := os.Args
	os.Args = []string{"go_openharmony_exec", bin, "-test.run=TestX"}
	defer func() { os.Args = args }()

	// A regular file is what noteToHuman reads as "no human is watching", which
	// is the same answer it gives under `go test`. The sync below fails, so the
	// notice it would otherwise print is exactly what must not land here.
	stderrFile := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(stderrFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	savedStderr := os.Stderr
	os.Stderr = f
	// Restored before the deferred Close above runs, and before any t.Fatalf
	// that would otherwise report into the captured file.
	defer func() { os.Stderr = savedStderr }()

	// The wrapper's exit status is the remote one, so a push that fails shows
	// up here as 125 rather than as a pass.
	if code := runMain(); code != 0 {
		t.Fatalf("runMain() = %d, want 0", code)
	}

	if said, err := os.ReadFile(stderrFile); err != nil {
		t.Fatal(err)
	} else if len(said) != 0 {
		t.Errorf("wrapper wrote %d bytes to stderr, want none: %q", len(said), said)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logged)

	// Read after Chdir: on macOS the kernel resolves symlinks, so the working
	// directory as the wrapper sees it is the only correct operand.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	deviceCwd := path.Join(deviceRoot, "pkg.test-"+strconv.Itoa(os.Getpid()), "cwd", filepath.ToSlash(cwd))

	// hdc's destination is the mirror of cwd's parent, so the parent's basename
	// reappears under it and the copy lands exactly at deviceCwd.
	if !strings.Contains(log, "file send "+cwd+" "+path.Dir(deviceCwd)) {
		t.Errorf("package directory not pushed to device; hdc invocations:\n%s", log)
	}
	// The cd is a guard, not a bare &&: a device directory that the host
	// believes is current but the device has lost must produce a wrapper
	// failure (via the missing sentinel) rather than a fake test exit status.
	if !strings.Contains(log, "cd "+shellQuote(deviceCwd)+" || ") {
		t.Errorf("binary not run from the mirrored package directory; hdc invocations:\n%s", log)
	}
}

// TestHDCFailureOnStdout covers hdc's habit of reporting a failure on stdout
// while still exiting zero. Trusting the exit status makes every setup step fail
// silently, and the push above all: the test then runs without its sources and
// looks like a port bug.
func TestHDCFailureOnStdout(t *testing.T) {
	dir := t.TempDir()
	stub := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if err := hdc(stub("ok", `echo "[Info]FileTransfer finish"`), "t", "file", "send", "a", "b"); err != nil {
		t.Errorf("hdc() = %v, want nil for ordinary output", err)
	}

	err := hdc(stub("fail", `echo "[Fail]Error opening file: no such file or directory"`), "t", "file", "send", "a", "b")
	if err == nil {
		t.Fatal("hdc() = nil, want an error: hdc exits 0 on failure, so its stdout is the only signal")
	}
	if !strings.Contains(err.Error(), "file send a b") {
		t.Errorf("error %q does not name the command that failed", err)
	}
}
