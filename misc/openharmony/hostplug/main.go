// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command hostplug loads the shared object built from ../plug with
// -buildmode=plugin and calls its exported Add.
//
//	hostplug <path-to-plug.so>
//
// It prints "Add(40,2)=42" and exits 0 on success.
package main

import (
	"fmt"
	"os"
	"plugin"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hostplug <plugin.so>")
		os.Exit(2)
	}

	p, err := plugin.Open(os.Args[1])
	if err != nil {
		fmt.Println("plugin.Open:", err)
		os.Exit(1)
	}
	sym, err := p.Lookup("Add")
	if err != nil {
		fmt.Println("Lookup:", err)
		os.Exit(1)
	}
	fn, ok := sym.(func(int, int) int)
	if !ok {
		fmt.Printf("Add has type %T, want func(int, int) int\n", sym)
		os.Exit(1)
	}

	got := fn(40, 2)
	fmt.Printf("Add(40,2)=%d\n", got)
	if got != 42 {
		os.Exit(1)
	}
}
