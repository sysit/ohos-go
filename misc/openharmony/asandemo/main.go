// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command asandemo writes one byte past the end of a heap allocation, in C.
//
//	GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1 CC=... \
//	    go build -asan -o /tmp/asandemo ./openharmony/asandemo
//	misc/openharmony/ohosrun /tmp/asandemo
//
// Built without -asan it exits 0 and says so. Built with -asan it must abort
// with "ERROR: AddressSanitizer: heap-buffer-overflow" — a successful build is
// not evidence that ASan works, only a detected overflow is.
//
// The overflow lives in C rather than Go because -asan only instruments the
// C/C++/assembly that cgo compiles; Go code is never instrumented.
package main

/*
#include <stdlib.h>
#include <stdio.h>

// ASan's runtime only exists when the C is compiled with -fsanitize=address, so
// the reference to it is gated on the compiler's own feature macro.
//
// A hand-declared __attribute__((weak)) symbol does NOT work for this: an
// undefined function reference needs weak_import to link on Mach-O, so the
// un-instrumented control build failed to link on the host instead of compiling
// as this file's contract says it does.
#if defined(__SANITIZE_ADDRESS__)
#define HAVE_ASAN 1
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
#define HAVE_ASAN 1
#endif
#endif

#ifdef HAVE_ASAN
extern int __asan_address_is_poisoned(void const volatile *addr);
#endif

static int boom(void) {
	char *p = malloc(4);
	if (p == NULL) {
		return -1;
	}
	// If the malloc interceptor is live, the allocator came from ASan and the
	// byte after the requested 4 is redzone. If it prints 0, libc served the
	// request and no redzone was ever laid down — which alone explains why the
	// store check below has a clean shadow byte to read.
#ifdef HAVE_ASAN
	printf("address_is_poisoned(p+4) = %d\n",
	    __asan_address_is_poisoned(p + 4));
#endif
	// volatile is load-bearing. Go's default CGO_CFLAGS is "-O2 -g", and at -O2
	// clang folds "p[4] = 1; v = p[4]" into the constant 1 and deletes the
	// store — malloc and free survive, the out-of-bounds write does not, and
	// there is nothing left for ASan to detect. A volatile store can be neither
	// folded nor removed.
	volatile char *q = p + 4;
	*q = 1; // heap-buffer-overflow
	free(p);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
)

func init() { println("asandemo: init reached") }

func main() {
	println("asandemo: main entered")
	v := C.boom()
	fmt.Printf("no overflow detected (read back %d)\n", int(v))
	os.Exit(0)
}
