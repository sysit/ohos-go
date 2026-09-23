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
// pushes the test binary to the device, puts the host GOROOT there too so that
// tests can read the source tree and run a device-native 'go', runs the binary
// from the corresponding directory of that tree, and reports its exit status as
// if it had run locally.
//
// Environment:
//
//	OHOS_HDC	path to the hdc binary (default: hdc, i.e. expected on PATH)
//	OHOS_TARGET	hdc -t target (default: 127.0.0.1:5555, the emulator)
//	GOOS, GOARCH	the platform under test (default: openharmony/this host's
//			architecture); they name the device-native bin and tool
//			directories inside GOROOT
//
// OHOS_HDC_TARGET is accepted as an alias for OHOS_TARGET. The commercial
// ("undebuggable") real devices refuse to exec binaries under /data/local/tmp
// from the shell domain, so the emulator is the default.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const (
	deviceRoot   = "/data/local/tmp/go_openharmony_exec"
	deviceGoroot = deviceRoot + "/goroot"
	deviceGoWork = deviceRoot + "/gowork"
	exitStr      = "__EXIT__"
	lockFile     = "go_openharmony_exec.lock"
	// syncStatusFile records the GOROOT revision already pushed. It lives on the
	// host, not the device, so that a tree edited on the host is re-pushed
	// rather than silently tested in its old form. The target is part of the
	// name because the record is per device: the tree at the same device path is
	// not the same tree, and a run against a second device must not be told
	// otherwise.
	syncStatusFile = "go_openharmony_exec-goroot-sync"
)

// forwardedEnv are host variables carried across to the device. The device
// shell starts clean, so anything the run was configured with on the host has
// to be named here to survive the crossing. These are the ones a test run
// legitimately sets and cannot re-derive on the device: the GODEBUG that
// selects FIPS mode, the limits that keep a package under the device's memory
// ceiling, and the cert bundle that stands in for the missing /etc/ssl/certs.
// GOOS/GOARCH/CGO_ENABLED are deliberately absent: they describe how to build
// for the device, and the device's own go computes them correctly by itself.
var forwardedEnv = []string{
	"GODEBUG", "GOGC", "GOMEMLIMIT", "GOMAXPROCS", "GOTRACEBACK", "SSL_CERT_FILE",
}

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

	// Before anything is pushed, make sure the device has the tree the tests
	// read from and the toolchain they exec. Best effort: a host with no GOROOT
	// (a -trimpath build) still runs everything that does not need one.
	hostGoroot, err := syncGoroot(hdcPath, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go_openharmony_exec: goroot sync: %v\n", err)
	}

	// Unique per process: two builds of the same package (different modules)
	// would otherwise race on one remote path.
	work := fmt.Sprintf("%s/%s-%d", deviceRoot, path.Base(bin), os.Getpid())
	// mkdir -p creates deviceRoot too, and work must exist before the binary is
	// pushed into it: hdc does not create missing parents.
	if err := hdc(hdcPath, target, "shell", "mkdir -p "+path.Join(work, "tmp")); err != nil {
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

	// Run where the test expects to be: inside the pushed GOROOT when the
	// package is part of it, otherwise in a mirror of the package directory.
	deviceCwd, err := deviceCwdFor(hostGoroot, hdcPath, target, work)
	if err != nil {
		return fail("push sources", err)
	}

	quoted := make([]string, 0, len(os.Args))
	quoted = append(quoted, remote)
	for _, a := range os.Args[2:] {
		quoted = append(quoted, shellQuote(a))
	}
	cmdline := strings.Join(quoted, " ")
	if deviceCwd != "" {
		cmdline = "cd " + deviceCwd + " && " + cmdline
	}
	cmdline = deviceEnv(hostGoroot, work) + cmdline

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

// syncGoroot makes the host GOROOT available on the device and returns its host
// path, or "" if it could not be arranged.
//
// This is the openharmony counterpart of go_android_exec's adbCopyGoroot, and
// it exists for the same two reasons. First, tests read from the source tree:
// internal/zstd opens ../../testdata, time loads ../../lib/time/zoneinfo.zip,
// io/ioutil lists the files next to its own, internal/copyright walks all of
// GOROOT/src. Second, tests that want to build a program need a 'go' on the
// device to build it with -- testenv probes 'go tool -n compile', and when that
// fails it skips, which is how most of go/build and go/types and ~200 of
// runtime's cases leave a run without ever showing up as a failure.
//
// The copy is a tarball rather than a tree walk: hdc costs about 6ms per file,
// so the 12k files of src+lib+bin+pkg/tool take ~3 minutes as individual sends
// and under a second as one compressed archive. The device has tar and gzip,
// and one archive also has one error to report instead of twelve thousand.
//
// Failure is not fatal. A host with no GOROOT (a -trimpath build) or a device
// that refuses the push leaves the wrapper behaving exactly as it did before
// the tree existed: mirror the package directory, run, and let the tests that
// need more skip.
func syncGoroot(hdcPath, target string) (string, error) {
	goroot, err := findGoroot()
	if err != nil {
		return "", err
	}
	statusPath := filepath.Join(os.TempDir(), syncStatusFile+"-"+target)
	want, err := gorootFingerprint(goroot)
	if err != nil {
		return "", err
	}
	// lock() serialises whole runs, so this check and the copy below cannot
	// interleave with another wrapper's.
	if b, err := os.ReadFile(statusPath); err == nil && string(b) == want {
		return goroot, nil
	}

	goos, goarch := targetPlatform()
	// A plain make.bash does not cross-build the commands, so the device-native
	// go may not exist yet. Build it here, once, rather than leave every test
	// that shells out to 'go build' to skip -- the skip is silent and hides more
	// cases than any single failure in a run.
	targetBin := filepath.Join(goroot, "bin", goos+"_"+goarch)
	if _, err := os.Stat(targetBin); err != nil {
		fmt.Fprintf(os.Stderr, "go_openharmony_exec: building the %s/%s toolchain (one time)\n", goos, goarch)
		cmd := exec.Command(filepath.Join(goroot, "bin", "go"), "install", "cmd")
		cmd.Dir = filepath.Join(goroot, "src")
		// CGO_ENABLED=0 because the device has no C toolchain to link against,
		// and a pure-Go go command is all the tests need it for.
		cmd.Env = append(os.Environ(),
			"GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=", "GOPROXY=off")
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("go install cmd: %w", err)
		}
	}

	// The archive layout has to match what the device's go expects to find:
	// the mirror entries at the GOROOT root, the commands in bin so that PATH
	// finds them, the compiler in pkg/tool/<goos>_<goarch> where go looks for
	// it, and pkg/include for the assembler's #includes.
	archives := []struct {
		name   string
		tarSrc []string // arguments after "tar czf <file>"
		into   string   // device directory to extract into
	}{
		{"goroot", append([]string{"-C", goroot}, mirrorEntries(goroot)...), deviceGoroot},
		{"bin", []string{"-C", targetBin, "."}, path.Join(deviceGoroot, "bin")},
		{"pkg", []string{"-C", filepath.Join(goroot, "pkg"), "include", path.Join("tool", goos+"_"+goarch)}, path.Join(deviceGoroot, "pkg")},
	}

	if err := hdc(hdcPath, target, "shell", "mkdir -p "+deviceRoot); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "go_openharmony_exec-sync")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	// Extract in one shell, after every archive has arrived: a partial tree is
	// worse than none, since it makes the tests that read it fail rather than
	// skip.
	script := "rm -rf " + deviceGoroot + " " + deviceGoWork +
		"; mkdir -p " + deviceGoroot + "/bin " + deviceGoroot + "/pkg" +
		" " + deviceGoWork + "/gocache " + deviceGoWork + "/gopath " + deviceGoWork + "/home"
	var remote []string
	for _, a := range archives {
		local := filepath.Join(tmp, a.name+".tgz")
		tarCmd := exec.Command("tar", append([]string{"czf", local}, a.tarSrc...)...)
		tarCmd.Stderr = os.Stderr
		if err := tarCmd.Run(); err != nil {
			return "", fmt.Errorf("tar %s: %w", a.name, err)
		}
		remote = append(remote, path.Join(deviceRoot, "sync-"+a.name+".tgz"))
		if err := hdc(hdcPath, target, "file", "send", local, remote[len(remote)-1]); err != nil {
			return "", err
		}
		script += "; tar xzf " + remote[len(remote)-1] + " -C " + a.into
	}
	if err := hdc(hdcPath, target, "shell", script+"; rm -f "+strings.Join(remote, " ")); err != nil {
		return "", err
	}

	if err := os.WriteFile(statusPath, []byte(want), 0600); err != nil {
		return "", err
	}
	return goroot, nil
}

// gorootFingerprint identifies the pushed tree well enough to notice an edit.
// 'go version' alone cannot do it: it stays go1.27.1 across a rebuild, while
// the sources under GOROOT/src change constantly in a toolchain checkout. A
// stale device tree makes a test read the *old* file, which is a wrong answer
// rather than a missing one -- the failure mode this port keeps producing.
//
// Walking src+lib is ~12k stats, paid once per go test run: the caller writes
// the fingerprint to a file on the host that every later invocation compares
// against.
func gorootFingerprint(goroot string) (string, error) {
	h := sha256.New()
	for _, entry := range mirrorEntries(goroot) {
		if err := fingerprintTree(h, filepath.Join(goroot, entry)); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// mirrorEntries is the set of top-level GOROOT entries the device gets, and
// what the sync fingerprint covers -- one list, so the cache cannot claim to be
// current for a tree it would not copy today.
//
// src and lib are what tests read directly. test and VERSION are not needed to
// run a test, but their absence turns skips into failures: go/types type-checks
// GOROOT/test/ken, and its guard only skips when test/ is absent *and* VERSION
// is present; cmd/dist -- which crypto and internal/platform reach through
// 'go tool dist list' -- reads VERSION and falls back to 'git log' without it,
// and the device has no git.
//
// pkg is not here: only include and tool/<target> are needed, and the rest of
// the host's pkg is 468MB of build cache the device has no use for.
func mirrorEntries(goroot string) []string {
	var entries []string
	for _, entry := range []string{"src", "lib", "test", "VERSION"} {
		// A tree that lacks one of these (a -trimpath build, a strange
		// distribution layout) must still sync the rest.
		if _, err := os.Stat(filepath.Join(goroot, entry)); err == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

func fingerprintTree(h hash.Hash, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %d %d\n", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
}

// findGoroot locates the host GOROOT.
//
// The wrapper's own location answers first: dist installs it at
// $GOROOT/bin/go_<goos>_<goarch>_exec, which is the same rule bin/go uses and
// the reason an unpacked tarball is relocatable at all. runtime.GOROOT() is
// only a fallback, because it is the path baked in at build time -- right for
// the tree it was built in, wrong the moment that tree is shipped to someone
// else, which is the case this wrapper exists to serve.
//
// Upstream's go_android_exec asks runtime.GOROOT() first and gets away with it:
// releases are built with -trimpath, so that answer is empty and the 'go env'
// fallback is what actually runs. This wrapper was not trimmed, so the baked
// path won every time -- a tree unpacked anywhere but the builder's directory
// then quietly got the wrong GOROOT (sync mirrored the baked path, or nothing,
// when it did not exist at all) and every test reading ../../testdata or
// ../../lib/time/zoneinfo.zip failed with ENOENT rather than being reported as
// a harness gap. runtime.GOROOT() is not consulted at all here: the toolchain
// deprecates it for this exact reason ("the root used during the Go build will
// not be meaningful if the binary is copied to another machine").
func findGoroot() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if root, ok := gorootFromExecutable(exe); ok {
			return root, nil
		}
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("cannot locate GOROOT: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", fmt.Errorf("cannot locate GOROOT")
	}
	return p, nil
}

// gorootFromExecutable is the GOROOT implied by the wrapper's own path, which is
// $GOROOT/bin/go_<goos>_<goarch>_exec. Split out from findGoroot because it is
// the decision that was wrong -- see the comment there -- and a decision that
// already shipped broken once is worth a test that does not need a device.
func gorootFromExecutable(exe string) (string, bool) {
	root := filepath.Dir(filepath.Dir(exe))
	return root, isGoroot(root)
}

// isGoroot reports whether dir is the root of a Go tree.
//
// src and VERSION are the pair to check: they are the entries mirrorEntries
// treats as the minimum, and VERSION is the one that tells a real tree from a
// directory that merely happens to contain a src/. A false positive here would
// point the mirror above the tree instead of at it -- worse than no answer,
// because it looks like one.
func isGoroot(dir string) bool {
	for _, entry := range []string{"src", "VERSION"} {
		if _, err := os.Stat(filepath.Join(dir, entry)); err != nil {
			return false
		}
	}
	return true
}

// targetPlatform is the platform being tested, which is what names the
// device-native bin and tool directories. go test exports GOOS and GOARCH to
// the wrapper; the default is the openharmony target on this host's
// architecture, since this wrapper exists for no other platform.
func targetPlatform() (goos, goarch string) {
	goos, goarch = os.Getenv("GOOS"), os.Getenv("GOARCH")
	if goos == "" {
		goos = "openharmony"
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// deviceEnv is the environment the test binary, and anything it execs, runs in.
//
// GOROOT has to be set or runtime.GOROOT() keeps returning the host path
// compiled into the binary, and every test that reads from GOROOT looks for a
// directory that only exists on the host. PATH is what makes 'go' findable at
// all -- testenv's LookPath("go") is the first thing that decides whether a
// build test can run. TMPDIR is per-run so that the temp files of one package
// are cleaned up with its work directory; GOCACHE is shared and persistent,
// because otherwise the device's go recompiles the standard library for every
// program a test asks it to build.
func deviceEnv(hostGoroot, work string) string {
	if hostGoroot == "" {
		return ""
	}
	env := "export GOROOT=" + deviceGoroot +
		"; export PATH=" + deviceGoroot + "/bin:$PATH" +
		"; export TMPDIR=" + path.Join(work, "tmp") +
		"; export GOCACHE=" + deviceGoWork + "/gocache" +
		"; export GOPATH=" + deviceGoWork + "/gopath" +
		"; export HOME=" + deviceGoWork + "/home" +
		// The device's go is the same version as the host's, so no toolchain
		// switch is needed and none is reachable without a network.
		"; export GOTOOLCHAIN=local" +
		"; export GOPROXY=" + shellQuote(proxyOrOff())
	for _, k := range forwardedEnv {
		if v := os.Getenv(k); v != "" {
			env += "; export " + k + "=" + shellQuote(v)
		}
	}
	return env + "; "
}

// proxyOrOff prefers the host's proxy setting and otherwise disables module
// fetching: the device is usually offline, and failing immediately beats the
// long timeout an unreachable proxy produces once per module lookup.
func proxyOrOff() string {
	if v := os.Getenv("GOPROXY"); v != "" {
		return v
	}
	return "off"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// deviceCwdFor returns the directory to run the test binary in.
//
// For a package inside GOROOT that is the same relative path inside the pushed
// tree, which is what makes ../testdata, ../../lib/time/zoneinfo.zip and
// GOROOT/src/... resolve the way the test expects -- and it costs nothing to
// arrange, because that tree is already on the device. Anything else (a module
// outside GOROOT) falls back to mirroring the package directory.
func deviceCwdFor(hostGoroot, hdcPath, target, work string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if hostGoroot != "" {
		if rel, err := filepath.Rel(hostGoroot, cwd); err == nil && !outside(rel) {
			return path.Join(deviceGoroot, filepath.ToSlash(rel)), nil
		}
	}
	return pushSourceTree(hdcPath, target, work)
}

// outside reports whether a relative path from GOROOT leaves it. filepath.Rel
// has no error to signal that: it answers with a path full of ".." instead.
func outside(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "."
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
// This is now the fallback: for a package inside GOROOT, deviceCwdFor runs from
// the pushed tree instead, which also gives the test the parent directories
// this mirror never had. Only a package outside GOROOT (a module being tested
// against this toolchain) still comes through here.
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
	glued := false
	if i := bytes.Index(l, []byte(exitStr)); i >= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(string(l[i+len(exitStr):]))); err == nil {
			f.code = n
			l = l[:i]
			glued = true
			if len(l) == 0 {
				return
			}
		}
	}
	l = bytes.TrimRight(l, "\r")
	// A glued line is the program's unterminated final output. The newline that
	// ended it came from the `echo` that reads the status, not from the program,
	// so re-adding one here would invent a byte the program never wrote:
	// cmd/internal/testdir's checkExpectedOutput compares the bytes exactly and
	// would report a mismatch the platform did not cause.
	if glued {
		f.w.Write(l)
		return
	}
	f.w.Write(append(l, '\n'))
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
