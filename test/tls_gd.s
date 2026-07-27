// objcheck -tls=GD

// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#define TLSBSS  256
GLOBL var·tls_g+0(SB), TLSBSS, $8

TEXT ·TlsVarAddr(SB),$0
// arm64:`.*\bADRP 0\(PC\), R0\s+\[0:8\]R_ARM64_TLS_GD:var.tls_g$`, `.*\bMOVD \(R0\), R27$`, `.*\bADD \$0, R0, R0$`, `.*\bCALL \(R27\)$`, -`.*R[3-91].*`
	MOVD	var·tls_g(SB), R0
	RET

