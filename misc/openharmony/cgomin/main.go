// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command cgomin is the smallest cgo program that can be built for
// openharmony: an empty preamble, no call into C. Its only job is to bisect a
// crash — if this runs and a cgo program that *does* call C does not, the
// fault is in the call; if this crashes too, it is in the cgo startup path
// (runtime/cgo's musl hooks) before any Go code runs.
package main

/*
 */
import "C"

import "fmt"

func init() { println("cgomin: init reached") }

func main() {
	fmt.Println("cgomin: main reached")
}
