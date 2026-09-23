// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGorootFromExecutable pins the rule that the wrapper's own path answers
// first, because that is the rule that shipped broken in beta2.
//
// The wrapper was built without -trimpath in that release, so runtime.GOROOT()
// inside it held the *builder's* directory. Preferring that answer meant an
// unpacked tarball anywhere else resolved every ../../testdata read against a
// path that does not exist on the user's machine, and the failure looked like a
// port bug rather than a harness gap. Nothing in the repo asserted the
// preference, so nothing caught it until the B layer was rerun against the
// artifact itself.
func TestGorootFromExecutable(t *testing.T) {
	// A tarball-shaped tree: VERSION next to src/ is what distinguishes a GOROOT
	// from a directory that merely contains a src/.
	root := t.TempDir()
	for _, entry := range []string{"src", "VERSION"} {
		if err := os.MkdirAll(filepath.Join(root, entry), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	exe := filepath.Join(root, "bin", "go_openharmony_arm64_exec")
	got, ok := gorootFromExecutable(exe)
	if !ok {
		t.Fatalf("gorootFromExecutable(%q) not ok, want the tree it sits in", exe)
	}
	if got != root {
		t.Errorf("gorootFromExecutable(%q) = %q, want %q", exe, got, root)
	}

	// Anything else must decline rather than guess: a wrong GOROOT is worse than
	// no answer, because the mirror then syncs the wrong tree -- or nothing --
	// and the tests fail somewhere far from the cause.
	for _, bad := range []string{
		filepath.Join(root, "go_exec"), // src/ and VERSION are two levels up, not one
		filepath.Join(t.TempDir(), "go_exec"),
	} {
		if root, ok := gorootFromExecutable(bad); ok {
			t.Errorf("gorootFromExecutable(%q) = %q, ok; want not ok", bad, root)
		}
	}
}
