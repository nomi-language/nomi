//go:build windows

package vmrunner

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// allocationGranularity is what a view's offset must be a multiple of on
// Windows.
const allocationGranularity = 64 << 10

// mapRange maps n bytes of f at off read-only, and answers them and how to
// unmap them: a file mapping of the whole file, and a view of it starting
// at the allocation unit holding off.
func mapRange(f *os.File, off, n int64) ([]byte, func() error, error) {
	start := off - off%allocationGranularity
	length := n + (off - start)
	if n <= 0 || int64(uintptr(length)) != length || int64(int(length)) != length {
		return nil, nil, errors.New("a range this platform cannot map")
	}
	h, err := syscall.CreateFileMapping(syscall.Handle(f.Fd()), nil, syscall.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return nil, nil, os.NewSyscallError("CreateFileMapping", err)
	}
	addr, err := syscall.MapViewOfFile(h, syscall.FILE_MAP_READ, uint32(start>>32), uint32(start), uintptr(length))
	if err != nil {
		syscall.CloseHandle(h)
		return nil, nil, os.NewSyscallError("MapViewOfFile", err)
	}
	// addr is the view's address outside Go's heap; reading it through a
	// pointer-typed word keeps vet's uintptr check satisfied.
	data := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), int(length))
	unmap := func() error {
		err := syscall.UnmapViewOfFile(addr)
		if cerr := syscall.CloseHandle(h); err == nil {
			err = cerr
		}
		return err
	}
	return data[off-start:], unmap, nil
}
