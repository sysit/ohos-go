// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command add exports a single function for the -buildmode=c-shared and
// -buildmode=c-archive smoke tests. Both modes take the same source.
package main

import "C"

//export Add
func Add(a, b C.int) C.int { return a + b }

func main() {}
