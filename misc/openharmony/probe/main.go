// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command probe reports facts about the OpenHarmony runtime environment from
// inside a Go process running on the device. It exists so that platform
// assumptions baked into the port (address-space width, where the system CA
// store lives, whether the resolver works) are checked against real hardware
// rather than inferred.
//
// Build and run from the repository root:
//
//	CGO_ENABLED=0 GOOS=openharmony GOARCH=arm64 ./bin/go build -o /tmp/probe ./misc/openharmony/probe
//	misc/openharmony/ohosrun /tmp/probe
//
// Every check is independent and time-bounded: one failure should not hide the
// others.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/bits"
	"net"
	"os"
	"runtime"
	"time"
	"unsafe"
)

const timeout = 5 * time.Second

func main() {
	fmt.Printf("goos=%s goarch=%s IsOpenharmony=%v\n",
		runtime.GOOS, runtime.GOARCH, runtime.IsOpenharmony)
	fmt.Printf("numcpu=%d gomaxprocs=%d version=%s\n",
		runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version())

	// The compiler and runtime assume a 39-bit user address space on
	// openharmony/arm64 (see heapAddrBits in runtime/malloc.go). Measure it
	// the same way a sanitizer would: from the top bit of a stack address.
	var stack int
	sp := uintptr(unsafe.Pointer(&stack))
	hp := uintptr(unsafe.Pointer(new(int)))
	fmt.Printf("vma_bits_stack=%d vma_bits_heap=%d stack=0x%x heap=0x%x\n",
		bits.Len64(uint64(sp)), bits.Len64(uint64(hp)), sp, hp)

	certPool()
	resolver()
	https()

	// Leave a marker so a truncated run (a sanitizer aborting before main,
	// say) is distinguishable from a run that got to the end.
	fmt.Println("probe: done")
}

// certPool reports whether x509 can find a system root store. A zero-length
// pool means every TLS connection to a public CA will fail verification
// unless SSL_CERT_FILE or SSL_CERT_DIR is set.
func certPool() {
	pool, err := x509.SystemCertPool()
	if err != nil {
		fmt.Printf("certpool: error: %v\n", err)
		return
	}
	if pool == nil {
		fmt.Println("certpool: nil pool, no error")
		return
	}
	fmt.Printf("certpool: %d certs\n", len(pool.Subjects()))

	for _, env := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v := os.Getenv(env); v != "" {
			fmt.Printf("certpool: %s=%s\n", env, v)
		}
	}
	for _, f := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/ssl/cert.pem",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/system/etc/security/cacerts",
	} {
		if _, err := os.Stat(f); err == nil {
			fmt.Printf("certpool: present %s\n", f)
		}
	}
}

// resolver reports whether the Go resolver can read a system configuration and
// resolve a name.
func resolver() {
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		fmt.Printf("dns: /etc/resolv.conf (%d bytes) %.120q\n", len(b), b)
	} else {
		fmt.Printf("dns: /etc/resolv.conf: %v\n", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		addrs, err := net.LookupHost("example.com")
		if err != nil {
			fmt.Printf("dns: LookupHost example.com: %v\n", err)
			return
		}
		fmt.Printf("dns: LookupHost example.com -> %v\n", addrs)
	}()
	wait(done, "dns: LookupHost timed out")
}

// https exercises the whole chain in one shot: DNS, TCP, TLS, and certificate
// verification against whatever root store the platform has.
func https() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		d := &net.Dialer{Timeout: timeout}
		conn, err := tls.DialWithDialer(d, "tcp", "example.com:443", nil)
		if err != nil {
			fmt.Printf("https: %v\n", err)
			return
		}
		defer conn.Close()
		st := conn.ConnectionState()
		fmt.Printf("https: ok peer=%q version=%x verified-chains=%d\n",
			st.PeerCertificates[0].Subject.CommonName,
			st.Version,
			len(st.VerifiedChains))
	}()
	wait(done, "https: timed out")
}

func wait(done <-chan struct{}, msg string) {
	select {
	case <-done:
	case <-time.After(timeout + time.Second):
		fmt.Println(msg)
	}
}
