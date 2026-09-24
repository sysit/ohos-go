// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncScriptStopsAtFirstFailure pins the shape of the device extract.
//
// Every stage has to be &&-joined. A tar that fails half way and lets the rest
// run leaves a tree that is partly the new revision and partly nothing, and the
// tests that read it then fail on device -- or worse, read a stale file and
// pass -- for reasons that have nothing to do with the port.
func TestSyncScriptStopsAtFirstFailure(t *testing.T) {
	archives := []archive{
		{"goroot", []string{"-C", "/g", "src"}, deviceGoroot},
		{"bin", []string{"-C", "/g/bin", "."}, path.Join(deviceGoroot, "bin")},
	}
	remote := []string{deviceRoot + "/sync-goroot.tgz", deviceRoot + "/sync-bin.tgz"}
	script := syncScript(archives, remote)

	for _, want := range []string{
		"&& tar xzf " + remote[0] + " -C " + deviceGoroot,
		"&& tar xzf " + remote[1] + " -C " + path.Join(deviceGoroot, "bin"),
		"&& echo " + syncOK,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script is missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "; tar xzf") {
		t.Errorf("a tar is ;-joined, so its failure would be lost:\n%s", script)
	}
	// The marker has to be the last stage of the chain: it is the evidence that
	// all of them ran, and hdc has nothing else to offer about the remote
	// shell's exit status.
	if strings.Index(script, "&& echo "+syncOK) < strings.Index(script, "tar xzf "+remote[1]) {
		t.Errorf("the marker is not the last stage:\n%s", script)
	}
	// Cleanup has to happen on both paths -- the tarballs are hundreds of
	// megabytes and the next run sends them again.
	if !strings.HasSuffix(script, "; rm -f "+strings.Join(remote, " ")) {
		t.Errorf("the tarballs are not removed after a failed extract too:\n%s", script)
	}
}

// TestCdGuard covers the failure that reads most like a port bug: a device
// directory the host believes is current but the device has lost. A bare
// "cd dir && cmd" reports the cd's status as the test's, so a whole layer goes
// red with nothing to explain it.
func TestCdGuard(t *testing.T) {
	const dir = "/data/local/tmp/work/My Project"
	got := cdGuard(dir)

	if !strings.Contains(got, "cd "+shellQuote(dir)) {
		t.Errorf("cdGuard(%q) = %q; the directory is not quoted", dir, got)
	}
	if strings.Contains(got, "&&") {
		t.Errorf("cdGuard(%q) = %q; a failed cd must not fall through to the command", dir, got)
	}
	if !strings.Contains(got, "exit 125") {
		t.Errorf("cdGuard(%q) = %q; a failed cd must leave the sentinel unwritten", dir, got)
	}
}

// TestOutside pins the GOROOT-membership test that chooses between running from
// the pushed tree and mirroring the package directory. Inverting it keeps every
// other test in this package green: the mirror is only reached through it.
func TestOutside(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{".", true},
		{"..", true},
		{path.Join("..", "elsewhere"), true},
		{"src", false},
		{path.Join("src", "net"), false},
	} {
		if got := outside(tc.rel); got != tc.want {
			t.Errorf("outside(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// TestDeviceCwdFor checks both arms of the choice TestOutside only half covers:
// inside GOROOT the run uses the tree already on the device and touches hdc not
// at all; outside it, the package directory has to be mirrored, which needs hdc
// and therefore fails without one.
func TestDeviceCwdFor(t *testing.T) {
	sub := filepath.Join(t.TempDir(), "src", "net")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	// Read after Chdir: on macOS the kernel resolves symlinks, so the answer the
	// wrapper will get from os.Getwd is the only correct operand for GOROOT too.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	goroot := filepath.Dir(filepath.Dir(cwd))

	got, err := deviceCwdFor(goroot, "no-such-hdc", "t", "/work")
	if err != nil {
		t.Fatalf("deviceCwdFor inside GOROOT: %v", err)
	}
	if want := path.Join(deviceGoroot, "src/net"); got != want {
		t.Errorf("deviceCwdFor inside GOROOT = %q, want %q", got, want)
	}

	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceCwdFor(goroot, "no-such-hdc", "t", "/work"); err == nil {
		t.Error("deviceCwdFor outside GOROOT did not reach the mirror")
	}
}

// syncFakeHDC returns a stub hdc that logs every invocation, and the log path to
// read afterwards. It echoes the sync marker only when ok, which is the whole of
// what the host can learn about a remote extract: hdc reports its own failures
// as "[Fail]" on stdout and says nothing whatever about the remote shell's.
func syncFakeHDC(t *testing.T, ok bool) (hdcPath, logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "hdc.log")
	script := "#!/bin/sh\n" + `echo "$@" >> "` + logPath + `"` + "\n"
	if ok {
		script += `case "$*" in *` + syncOK + `*) echo ` + syncOK + `;; esac` + "\n"
	}
	hdcPath = filepath.Join(t.TempDir(), "hdc")
	if err := os.WriteFile(hdcPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The sync cache lives in TMPDIR. Pointing it at the test's tree also keeps
	// a warm one from an earlier run out of the way.
	t.Setenv("TMPDIR", t.TempDir())
	return hdcPath, logPath
}

// TestSyncKeepsTheTreeWhenTheToolchainCannotBeBuilt is the acceptance for the
// one step of the sync that adds a capability instead of delivering the tree.
//
// That step cannot always succeed. openharmony/amd64 forces external linking,
// which wants cgo and a device C toolchain this build has neither of, so
// `go install cmd` for it fails on every tree that has not been built for the
// target yet. An arch that does not exist stands in for that: the failure takes
// the same shape and costs no toolchain build.
//
// Letting that failure end the sync takes the source-tree mirror down with it,
// and the mirror is what stops a test reading ../../testdata from failing with
// ENOENT and being read as a port bug. Losing only the device go makes those
// tests skip, which is coverage lost rather than credibility.
func TestSyncKeepsTheTreeWhenTheToolchainCannotBeBuilt(t *testing.T) {
	fakeHDC, logPath := syncFakeHDC(t, true)

	if _, err := syncGoroot(fakeHDC, "test-target", "openharmony", "no-such-arch"); err != nil {
		t.Fatalf("syncGoroot: %v", err)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logged)
	if !strings.Contains(log, "sync-goroot.tgz") {
		t.Errorf("the source tree was not sent:\n%s", log)
	}
	for _, absent := range []string{"sync-bin.tgz", "sync-pkg.tgz"} {
		if strings.Contains(log, absent) {
			t.Errorf("%s was sent for a toolchain that was never built:\n%s", absent, log)
		}
	}
}

// TestSyncDoesNotCacheAFailedExtract is the acceptance for the other half of the
// marker: an extract that did not report finishing must not be recorded as done.
//
// The record is a fingerprint on the host that makes every later run skip the
// copy, so caching a failure is permanent rather than merely wrong -- the device
// keeps its partial tree until someone edits GOROOT/src, and tests that read it
// answer from a stale file. That is a wrong answer where a retry would have been
// a right one.
func TestSyncDoesNotCacheAFailedExtract(t *testing.T) {
	fakeHDC, logPath := syncFakeHDC(t, false)

	// The arch is bad too, so the toolchain step fails as well; neither may be
	// mistaken for a finished sync.
	if _, err := syncGoroot(fakeHDC, "test-target", "openharmony", "no-such-arch"); err == nil {
		t.Fatal("syncGoroot accepted an extract that never reported finishing")
	}
	if logged, err := os.ReadFile(logPath); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(logged), "sync-goroot.tgz") {
		t.Errorf("the sync never got as far as sending the tree:\n%s", logged)
	}

	statusPath := filepath.Join(os.TempDir(), syncStatusFile+"-test-target")
	if _, err := os.Stat(statusPath); err == nil {
		t.Errorf("%s was written for an extract that did not finish; the next run would skip the copy", statusPath)
	}
}
