// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

#include <stddef.h>

extern char **environ;

// x_cgo_is_musl reports whether the C library is musl.
int x_cgo_is_musl() {
	#if defined(__GLIBC__) || defined(__UCLIBC__)
		return 0;
	#else
		return 1;
	#endif
}

int x_cgo_get_environ(char ***env_ptr) {
	if (env_ptr == NULL) {
		return -1;
	}
	if (!environ) {
		return -1;
	}
	*env_ptr = environ;
	return 0;
}
