// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// cdriver exercises Go code built with -buildmode=c-shared or
// -buildmode=c-archive.
//
//	cdriver dlopen <path-to-libadd.so>   load at runtime, look up "Add"
//	cdriver link                         call Add, resolved at link time
//
// Both paths print "Add(40,2)=42" and exit 0 on success. The dlopen path is
// the one that matters for OpenHarmony: a HAP cannot run a binary out of
// /data/local/tmp, but it can load a .so, so dlopen is how a Go c-shared
// library actually ships.
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

// The dlopen build must link with no archive present, so Add is declared weak
// there. The link build needs the opposite: an archive member is only pulled in
// to satisfy a *strong* undefined reference, so a weak declaration would leave
// Add unresolved and the archive untouched.
#ifdef CDRIVER_LINK_ADD
extern int Add(int a, int b);
#else
extern int Add(int a, int b) __attribute__((weak));
#endif

typedef int (*add_fn)(int, int);

int main(int argc, char **argv) {
	int got;

	if (argc == 3 && strcmp(argv[1], "dlopen") == 0) {
		void *h = dlopen(argv[2], RTLD_NOW);
		if (h == NULL) {
			fprintf(stderr, "dlopen: %s\n", dlerror());
			return 1;
		}
		add_fn fn = (add_fn)dlsym(h, "Add");
		if (fn == NULL) {
			fprintf(stderr, "dlsym: %s\n", dlerror());
			return 1;
		}
		got = fn(40, 2);
	} else {
		got = Add(40, 2);
	}

	printf("Add(40,2)=%d\n", got);
	return got == 42 ? 0 : 1;
}
