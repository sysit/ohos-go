// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command plug is built with -buildmode=plugin and loaded by hostplug. It
// exists to prove that plugin.Open works on OpenHarmony: that the
// linux && cgo build tag on runtime/plugin_dlopen.go is selected for the
// openharmony platform, and that musl's dlopen honours RTLD_NOW|RTLD_GLOBAL
// the way the Go runtime expects.
package main

func Add(a, b int) int { return a + b }

func main() {}
