// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#ifdef GOOS_openharmony
#define TLS_linux
#endif

#ifdef GOOS_android
#define TLS_linux
#define TLSG_IS_VARIABLE
#endif
#ifdef GOOS_linux
#define TLS_linux
#endif
#ifdef TLS_linux
#define MRS_TPIDR_R27 WORD $0xd53bd05b // MRS TPIDR_EL0, R27
#endif

#ifdef GOOS_darwin
#define TLS_darwin
#endif
#ifdef GOOS_ios
#define TLS_darwin
#endif
#ifdef TLS_darwin
#define TLSG_IS_VARIABLE
#define MRS_TPIDR_R27 WORD $0xd53bd07b // MRS TPIDRRO_EL0, R27
#endif

#ifdef GOOS_freebsd
#define MRS_TPIDR_R27 WORD $0xd53bd05b // MRS TPIDR_EL0, R27
#endif

#ifdef GOOS_netbsd
#define MRS_TPIDR_R27 WORD $0xd53bd05b // MRS TPIDRRO_EL0, R27
#endif

#ifdef GOOS_openbsd
#define MRS_TPIDR_R27 WORD $0xd53bd05b // MRS TPIDR_EL0, R27
#endif

#ifdef GOOS_windows
#define TLS_windows
#endif
#ifdef TLS_windows
#define TLSG_IS_VARIABLE
#define MRS_TPIDR_R27 MOVD R18_PLATFORM, R27
#endif

// Define something that will break the build if
// the GOOS is unknown.
#ifndef MRS_TPIDR_R27
#define MRS_TPIDR_R27 unknown_TLS_implementation_in_tls_arm64_h
#endif

#ifdef TLS_GD
// General dynamic TLS model: the assembler lowers second MOVD (that gets the tls
// variable's offset from thread pointer) to a 4-instruction sequence ADRP;LDR;ADD;BLR
// (the external linker can sometimes relax it / nop-out some of them).
// The result is returned in R0, and this macro is assumed to be used by functions
// without stack frame, so we clobber R25 (to temporarily keep the return address)
// and R27 (address of the __tlsdesc-stub).
// Move SP to make sure __tlsdesc-stub does not overwrite FP saved at SP-8.
#define LOAD_TLS_G_R0 \
    MOVD LR, R25 \
    SUB $16, RSP \
    MOVD runtime·tls_g(SB), R0 \
    ADD $16, RSP \
    MOVD R25, LR
#else
// Get TLS variable's offset (using initial exec or local exec TLS model) from
// the thread pointer, which can be obtained with LOAD_TP_R27 macro.
#define LOAD_TLS_G_R0 \
    MOVD runtime·tls_g(SB), R0
#endif

#ifdef TLS_darwin
// Darwin may return unaligned thread pointer. Align it.
#define LOAD_TP_R27 \
    MRS_TPIDR_R27 \
    AND   $-7, R27
#else
#define LOAD_TP_R27 \
    MRS_TPIDR_R27
#endif
