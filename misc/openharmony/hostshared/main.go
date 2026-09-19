// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command hostshared is built with -linkshared against the shared libraries
// installed for openharmony/arm64 (libstd.so from `go install -buildmode=shared
// std`, plus the shared form of ../libadd). It prints "Add(40,2)=42" and exits
// 0 on success.
//
// At run time both .so files must be findable by the loader, e.g.
//
//	LD_LIBRARY_PATH=/data/local/tmp/ohosrun hostshared
package main

import (
	"fmt"
	"os"

	"misc/openharmony/libadd"
)

func main() {
	got := add.Add(40, 2)
	fmt.Printf("Add(40,2)=%d\n", got)
	if got != 42 {
		os.Exit(1)
	}
}
