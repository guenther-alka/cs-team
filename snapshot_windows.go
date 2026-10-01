//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

// volumeLabel: Datenträgerbezeichnung des Laufwerks von path (z.B. "winpool" für D:\).
func volumeLabel(path string) string {
	vol := filepath.VolumeName(path)
	if vol == "" {
		return ""
	}
	root, err := syscall.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 261)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetVolumeInformationW")
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0, 0, 0, 0)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
