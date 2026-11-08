package state

import (
	"syscall"
	"unsafe"
)

var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(from, to string) error {
	f, e := syscall.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	t, e := syscall.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	r, _, e := moveFileEx.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), 1|8)
	if r == 0 {
		return e
	}
	return nil
}
