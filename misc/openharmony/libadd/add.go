// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package add is installed with -buildmode=shared so that hostshared can be
// built with -linkshared and pull Add out of a shared library at run time
// rather than linking it in. This fixture is what exercises -buildmode=shared,
// the last build mode the port had not covered: it depends on SONAME wiring and
// on the -Wl,-z,global workaround that exists because OHOS dlopen ignores
// RTLD_GLOBAL. Result is recorded in docs/go-upgrade-guide.md §4.2.
package add

func Add(a, b int) int { return a + b }
