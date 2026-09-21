// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

// Command go_openharmony_exec runs a cross-compiled binary on an OpenHarmony
// device or emulator over hdc. It is the openharmony counterpart of
// misc/go_android_exec, and dist installs it as $GOROOT/bin/go_openharmony_<arch>_exec
// so that the go command finds it by the usual go_<GOOS>_<GOARCH>_exec
// convention:
//
//	GOOS=openharmony GOARCH=arm64 go test ./pkg
//
// pushes the test binary to the device, mirrors the package's working
// directory alongside it, runs the binary there from that mirror, and reports
// its exit status as if it had run locally.
//
// Environment:
//
//	OHOS_HDC	path to the hdc binary (default: hdc, i.e. expected on PATH)
//	OHOS_TARGET	hdc -t target (default: 127.0.0.1:5555, the emulator)
//
// OHOS_HDC_TARGET is accepted as an alias for OHOS_TARGET. The commercial
// ("undebuggable") real devices refuse to exec binaries under /data/local/tmp
// from the shell domain, so the emulator is the default.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	deviceRoot = "/data/local/tmp/go_openharmony_exec"
	exitStr    = "__EXIT__"
	lockFile   = "go_openharmony_exec.lock"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go_openharmony_exec <binary> [args...]")
		return 2
	}
	bin := os.Args[1]

	hdcPath := os.Getenv("OHOS_HDC")
	if hdcPath == "" {
		hdcPath = "hdc"
	}
	target := os.Getenv("OHOS_TARGET")
	if target == "" {
		target = os.Getenv("OHOS_HDC_TARGET")
	}
	if target == "" {
		target = "127.0.0.1:5555"
	}

	// hdc is not safe to drive concurrently from several processes, and
	// `go test` runs packages in parallel.
	unlock, err := lock()
	if err != nil {
		return fail("lock", err)
	}
	defer unlock()

	// Unique per process: two builds of the same package (different modules)
	// would otherwise race on one remote path.
	work := fmt.Sprintf("%s/%s-%d", deviceRoot, path.Base(bin), os.Getpid())
	// mkdir -p creates deviceRoot too, and work must exist before the binary is
	// pushed into it: hdc does not create missing parents.
	if err := hdc(hdcPath, target, "shell", "mkdir -p "+work); err != nil {
		return fail("mkdir", err)
	}
	defer hdc(hdcPath, target, "shell", "rm -rf "+work)

	remote := path.Join(work, path.Base(bin))
	if err := hdc(hdcPath, target, "file", "send", bin, remote); err != nil {
		return fail("push", err)
	}
	if err := hdc(hdcPath, target, "shell", "chmod +x "+remote); err != nil {
		return fail("chmod", err)
	}

	// Run from a mirror of the package directory, so tests that read files
	// sitting next to their sources have something to read.
	deviceCwd, err := pushSourceTree(hdcPath, target, work)
	if err != nil {
		return fail("push sources", err)
	}

	quoted := make([]string, 0, len(os.Args))
	quoted = append(quoted, remote)
	for _, a := range os.Args[2:] {
		quoted = append(quoted, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	cmdline := strings.Join(quoted, " ")
	if deviceCwd != "" {
		cmdline = "cd " + deviceCwd + " && " + cmdline
	}

	code, err := run(hdcPath, target, cmdline)
	if err != nil {
		return fail("run", err)
	}
	return code
}

func fail(what string, err error) int {
	fmt.Fprintf(os.Stderr, "go_openharmony_exec: %s: %v\n", what, err)
	// 125 marks "the wrapper itself failed", as in go_android_exec, so it stays
	// distinguishable from a test binary that exited non-zero.
	return 125
}

// run executes cmdline on the device, forwarding its output to stdout and
// returning its exit status.
//
// hdc's own exit status is unreliable, so the remote shell echoes a sentinel
// after the command and we recover the status from the output stream. That is
// also why the sentinel is parsed rather than the exit code propagated: hdc
// multiplexes stdout and stderr onto one channel.
func run(hdcPath, target, cmdline string) (int, error) {
	// stderr is wrapped in a bare struct so that a wedged hdc cannot hold the
	// fd open and stall `go test`, which reads the child until EOF.
	cmd := exec.Command(hdcPath, append(hdcArgs(target), "shell", cmdline+"; echo "+exitStr+"$?")...)
	f := newExitFilter(os.Stdout)
	cmd.Stdout = f
	cmd.Stderr = struct{ io.Writer }{os.Stderr}
	err := cmd.Run()
	f.Finish()
	if f.code < 0 {
		return 0, fmt.Errorf("no exit code from target %s (is the device connected? "+
			"on a commercial device the shell domain cannot exec under /data/local/tmp)", target)
	}
	// A non-zero remote status is a legitimate result, not a wrapper failure,
	// so only a failure to start or to reach the device is an error here.
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return 0, err
		}
	}
	return f.code, nil
}

// pushSourceTree mirrors the wrapper's working directory onto the device and
// returns the path to run the test binary from, or "" if there is nothing to
// mirror.
//
// `go test` runs this wrapper with its working directory set to the package
// directory, which is the only handle we have on where the test expects to be.
// Without the mirror, tests that read files next to their sources fail for
// reasons that have nothing to do with the port: os looks for stat_linux.go in
// the working directory, io/fs's TestGlob walks it, and text/template opens
// testdata/. go_android_exec solves the same problem with adbCopyTree.
//
// The host directory is mirrored under work/cwd, so the device layout is a copy
// of the host one and no import-path bookkeeping is needed. hdc copies
// directories recursively, so a single call brings the sources and the
// package's own testdata across.
//
// ponytail: only the package directory itself is copied, not the testdata of
// parent packages. go_android_exec walks the tree upwards for that; add the
// same walk here if a test turns out to read ../testdata.
func pushSourceTree(hdcPath, target, work string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if cwd == string(filepath.Separator) {
		// Nothing useful lives at the root; mirroring it would produce a
		// one-component path and copy the whole filesystem.
		return "", nil
	}
	deviceCwd := path.Join(work, "cwd", filepath.ToSlash(cwd))
	if err := hdc(hdcPath, target, "shell", "mkdir -p "+path.Dir(deviceCwd)); err != nil {
		return "", err
	}
	if err := hdc(hdcPath, target, "file", "send", cwd, path.Dir(deviceCwd)); err != nil {
		return "", err
	}
	return deviceCwd, nil
}

// exitFilter forwards the remote output to stdout, withholding the sentinel
// line and recovering the exit status from it. Output is processed a line at a
// time because hdc line-buffers; the trailing partial line is held until
// Finish so that a split sentinel is still recognised.
type exitFilter struct {
	w    io.Writer
	buf  []byte
	code int
}

// newExitFilter returns a filter that reports no status yet. The -1 initial
// value is what keeps a run that never reports one from being read as a success:
// hdc exits non-zero when it cannot reach the device, but that arrives as an
// *exec.ExitError, which run deliberately does not propagate, so the sentinel is
// the only remaining signal. Constructing through here rather than a bare struct
// literal keeps that decision in one place for run and for the tests.
func newExitFilter(w io.Writer) *exitFilter {
	return &exitFilter{w: w, code: -1}
}

func (f *exitFilter) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		i := bytes.IndexByte(f.buf, '\n')
		if i < 0 {
			break
		}
		f.line(f.buf[:i])
		f.buf = f.buf[i+1:]
	}
	return len(p), nil
}

func (f *exitFilter) Finish() {
	if len(f.buf) > 0 {
		f.line(f.buf)
		f.buf = nil
	}
}

func (f *exitFilter) line(l []byte) {
	// A test binary that ends without a trailing newline glues its last output
	// to the sentinel, so look for the marker anywhere in the line.
	if i := bytes.Index(l, []byte(exitStr)); i >= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(string(l[i+len(exitStr):]))); err == nil {
			f.code = n
			l = l[:i]
			if len(l) == 0 {
				return
			}
		}
	}
	f.w.Write(append(bytes.TrimRight(l, "\r"), '\n'))
}

// hdc runs a setup command on the device, discarding its chatter unless it
// failed.
//
// hdc reports failures on stdout as a "[Fail]" line and *still exits zero*: a
// `file send` into a directory that does not exist prints
// "[Fail]Error opening file: no such file or directory" and exits 0. The exit
// status alone therefore proves nothing, and without this check a failed push
// degrades silently into "the test ran but its data was missing" -- exactly the
// harness defect that is indistinguishable from a port bug.
func hdc(hdcPath, target string, args ...string) error {
	cmd := exec.Command(hdcPath, append(hdcArgs(target), args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if bytes.Contains(out.Bytes(), []byte("[Fail]")) {
		return fmt.Errorf("hdc %s: %s", strings.Join(args, " "), strings.TrimSpace(out.String()))
	}
	return nil
}

func hdcArgs(target string) []string {
	if target == "" {
		return nil
	}
	return []string{"-t", target}
}

// lock takes an exclusive advisory lock so that concurrent invocations
// serialise their hdc use. It returns the release function.
func lock() (func(), error) {
	f, err := os.OpenFile(path.Join(os.TempDir(), lockFile), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
