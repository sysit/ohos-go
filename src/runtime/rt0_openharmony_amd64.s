// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

TEXT _rt0_amd64_openharmony(SB),NOSPLIT,$-8
	JMP	_rt0_amd64(SB)

// When building with -buildmode=c-shared, this symbol is called when the shared
// library is loaded. On amd64 the generic _rt0_amd64_lib already saves the
// callee-saved registers and defers initialization to a new thread, the same
// way linux and android do; argc/argv arrive in DI/SI from the .init_array
// call made by the musl loader.
TEXT _rt0_amd64_openharmony_lib(SB),NOSPLIT,$0
	JMP	_rt0_amd64_lib(SB)
