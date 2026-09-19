// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package add is installed with -buildmode=shared so that hostshared can be
// built with -linkshared and pull Add out of a shared library at run time
// rather than linking it in. -buildmode=shared is the one build mode the
// OpenHarmony port has never exercised: it depends on SONAME wiring and on the
// -Wl,-z,global workaround that exists because OHOS dlopen ignores
// RTLD_GLOBAL.
package add

func Add(a, b int) int { return a + b }
