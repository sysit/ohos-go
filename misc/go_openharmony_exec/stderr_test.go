// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"testing"
)

// TestNoteToHuman pins that a routine notice is withheld unless a human is
// reading the stream.
//
// The C layer caught the alternative on the artifact, not in the dev tree: a
// freshly unpacked tree has no device toolchain, so the wrapper announced the
// one-time build on stderr, testdir compares stdout and stderr in one buffer,
// and abi/open_defer_1.go failed over output the program never produced. The
// dev tree hid it because earlier runs had already built that toolchain, and
// runtests.sh would have hidden it differently -- it reads a bare
// "go_openharmony_exec:" line as a wrapper failure.
func TestNoteToHuman(t *testing.T) {
	// A pipe: the shape every capture has, and the shape testdir gives the
	// wrapper.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if noteToHuman(w) {
		t.Error("noteToHuman(pipe) = true, want false: a captured stream is not a terminal")
	}

	// A regular file: what `go test > log` and a redirected 2> give.
	f, err := os.Create(t.TempDir() + "/stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if noteToHuman(f) {
		t.Errorf("noteToHuman(%s) = true, want false: a regular file is not a terminal", f.Name())
	}

	// A character device: the interactive case the notice exists for. Without
	// this the test also passes on `return false`, which would delete the
	// message instead of scoping it.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	if !noteToHuman(devNull) {
		t.Errorf("noteToHuman(%s) = false, want true: it is a character device", os.DevNull)
	}
}
