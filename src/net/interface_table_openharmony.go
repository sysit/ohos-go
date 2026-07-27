// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cgo && openharmony

package net

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

type ifreq struct {
	Name [16]uint8
	Ifru [24]byte
}

func interfaceTable(ifindex int) ([]Interface, error) {
	// get all internet address
	var res *_C_struct_ifaddrs
	gerrno, err := _C_getifaddrs(&res)
	if gerrno != 0 || err != nil {
		return nil, os.NewSyscallError("_C_getifaddrs", errors.Join(err, errors.New(_C_gai_strerror(_C_int(gerrno)))))
	}

	// create a socket for ioctl syscall
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, os.NewSyscallError("Socket", err)
	}

	var ifts []Interface
	processed := make(map[int]int)
	for r := res; r != nil; r = *_C_ifa_next(r) {
		ifaAddr := *_C_ifa_addr(r)
		if ifaAddr == nil {
			continue
		}
		ift := Interface{
			Name:  _C_gifa_name(r),
			Flags: linkFlags(uint32(*_C_ifa_flags(r))),
		}

		ifr := &ifreq{}
		copy(ifr.Name[:], ift.Name)
		// retrieve index
		_, _, ep := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), syscall.SIOCGIFINDEX, uintptr(unsafe.Pointer(ifr)))
		if ep == 0 {
			ift.Index = int(*(*uint32)(unsafe.Pointer(&ifr.Ifru[:4][0])))
			if _, ok := processed[ift.Index]; ok {
				continue
			}
			if ifindex != 0 && ift.Index != ifindex {
				continue
			}
		}
		// retrieve mtu
		_, _, ep = syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), syscall.SIOCGIFMTU, uintptr(unsafe.Pointer(ifr)))
		if ep == 0 {
			ift.MTU = int(*(*uint32)(unsafe.Pointer(&ifr.Ifru[:4][0])))
		}
		// retrieve mac addr
		_, _, ep = syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), syscall.SIOCGIFHWADDR, uintptr(unsafe.Pointer(ifr)))
		if ep == 0 {
			// ifr_ifru.ifru_hwaddr.sa_data, skip address family and length(2 bytes).
			var nonzero bool
			for _, b := range ifr.Ifru[2:8] {
				if b != 0 {
					nonzero = true
					break
				}
			}
			if nonzero {
				ift.HardwareAddr = ifr.Ifru[2:8]
			}
		}

		ifts = append(ifts, ift)
		processed[ift.Index] = 1
	}

	return ifts, nil
}

func interfaceAddrTable(ifi *Interface) ([]Addr, error) {
	// get all internet address
	var res *_C_struct_ifaddrs
	gerrno, err := _C_getifaddrs(&res)
	if gerrno != 0 || err != nil {
		return nil, os.NewSyscallError("_C_getifaddrs", errors.Join(err, errors.New(_C_gai_strerror(_C_int(gerrno)))))
	}

	// create a socket for ioctl syscall
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, os.NewSyscallError("Socket", err)
	}

	var addrs []Addr
	for r := res; r != nil; r = *_C_ifa_next(r) {
		ifaAddr := *_C_ifa_addr(r)
		if ifaAddr == nil {
			continue
		}
		ifr := &ifreq{}
		copy(ifr.Name[:], _C_gifa_name(r))
		// retrieve index
		_, _, ep := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), syscall.SIOCGIFINDEX, uintptr(unsafe.Pointer(ifr)))
		if ep != 0 {
			continue
		}
		index := int(*(*uint32)(unsafe.Pointer(&ifr.Ifru[:4][0])))
		if ifi != nil && ifi.Index != index {
			continue
		}

		rsa := (*syscall.RawSockaddrAny)(unsafe.Pointer(ifaAddr))
		switch rsa.Addr.Family {
		case syscall.AF_INET:
			sa := (*syscall.RawSockaddrInet4)(unsafe.Pointer(rsa))
			ipv4 := &IPNet{IP: IPv4(sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3])}

			maskAddr := *_C_ifa_mask(r)
			if maskAddr != nil {
				maskSa := (*syscall.RawSockaddrInet4)(unsafe.Pointer(maskAddr))
				ipv4.Mask = CIDRMask(simpleMaskLength(maskSa.Addr[:]), 8*IPv4len)
			}
			addrs = append(addrs, ipv4)
		case syscall.AF_INET6:
			sa := (*syscall.RawSockaddrInet6)(unsafe.Pointer(rsa))
			ipv6 := &IPNet{IP: make(IP, IPv6len)}
			copy(ipv6.IP, sa.Addr[:])
			maskAddr := *_C_ifa_mask(r)
			if maskAddr != nil {
				maskSa := (*syscall.RawSockaddrInet6)(unsafe.Pointer(maskAddr))
				ipv6.Mask = CIDRMask(simpleMaskLength(maskSa.Addr[:]), 8*IPv6len)
			}
			addrs = append(addrs, ipv6)
		}
	}

	return addrs, nil
}
