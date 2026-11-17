package state

import (
	"syscall"
	"time"
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
	// Readers and antivirus scanners can briefly hold a handle without delete
	// sharing. Retry the same atomic move for at most one second; never remove
	// the old snapshot to work around a lock or a real permission failure.
	for attempt := 0; ; attempt++ {
		r, _, e := moveFileEx.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), 1|8)
		if r != 0 {
			return nil
		}
		if attempt >= 40 || (e != syscall.ERROR_ACCESS_DENIED && e != syscall.Errno(32) && e != syscall.Errno(33)) {
			return e
		}
		time.Sleep(25 * time.Millisecond)
	}
}
