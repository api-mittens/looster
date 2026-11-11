//go:build windows

package ducklake

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var payloadLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func lockPayloadFile(f *os.File) error {
	var overlapped syscall.Overlapped
	// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY. The OS releases this
	// byte-range lock when the handle closes or its owning process exits.
	ok, _, err := payloadLockFileEx.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return err
	}
	return nil
}
func payloadLockBusy(err error) bool { return errors.Is(err, syscall.Errno(33)) }

// Windows does not support fsync on directory handles opened by os.Open.
// Payload files themselves are flushed and Put still requires a remote commit,
// which is the authoritative durable acknowledgement on every platform.
func syncPayloadDir(string) error { return nil }
