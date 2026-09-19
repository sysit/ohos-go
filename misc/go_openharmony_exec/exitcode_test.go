// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

// TestExitFilter feeds the filter the shapes of output hdc actually produces and
// checks both the recovered status and what was forwarded to the user.
func TestExitFilter(t *testing.T) {
	testCases := []struct {
		name string
		// writes are delivered as separate Write calls, mirroring hdc's
		// line-buffered reads, so that split sentinels are exercised.
		writes []string
		code   int
		out    string
	}{
		{
			name:   "success",
			writes: []string{"hello\n", "__EXIT__0\n"},
			code:   0,
			out:    "hello\n",
		},
		{
			name:   "failure status is propagated",
			writes: []string{"--- FAIL: TestX\n", "__EXIT__1\n"},
			code:   1,
			out:    "--- FAIL: TestX\n",
		},
		{
			// A binary that ends without a trailing newline glues its last
			// output to the sentinel; that output must survive.
			name:   "sentinel glued to output",
			writes: []string{"boom__EXIT__3"},
			code:   3,
			out:    "boom\n",
		},
		{
			name:   "sentinel split across writes",
			writes: []string{"__EXIT", "__4\n"},
			code:   4,
			out:    "",
		},
		{
			// hdc line endings; the \r must not leak into the output.
			name:   "crlf line endings",
			writes: []string{"hello\r\n", "__EXIT__0\r\n"},
			code:   0,
			out:    "hello\n",
		},
		{
			// The regression this filter exists for: nothing ever reported a
			// status, so the run must not be mistaken for a success. A device
			// that refuses to exec, or one that is not connected, lands here.
			name:   "no status reported",
			writes: []string{"partial output\n"},
			code:   -1,
			out:    "partial output\n",
		},
		{
			name:   "no output at all",
			writes: nil,
			code:   -1,
			out:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			f := newExitFilter(&buf)
			for _, w := range tc.writes {
				if _, err := f.Write([]byte(w)); err != nil {
					t.Fatalf("Write(%q): %v", w, err)
				}
			}
			f.Finish()

			if f.code != tc.code {
				t.Errorf("exit code = %d, want %d", f.code, tc.code)
			}
			if got := buf.String(); got != tc.out {
				t.Errorf("forwarded output = %q, want %q", got, tc.out)
			}
		})
	}
}
