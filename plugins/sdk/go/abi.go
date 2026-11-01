//go:build wasip1

package pyro

import (
	"unsafe"
)

// ABI v1. The host writes a JSON message into memory obtained from
// pyro_alloc, calls pyro_invoke, reads the JSON reply at the returned
// (ptr<<32 | len) and releases it with pyro_free.

// pinned keeps buffers handed to the host alive until it frees them.
var pinned = map[uint32][]byte{}

func ptrOf(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

//go:wasmexport pyro_alloc
func pyroAlloc(size uint32) uint32 {
	if size == 0 {
		size = 1
	}
	b := make([]byte, size)
	p := ptrOf(b)
	pinned[p] = b
	return p
}

//go:wasmexport pyro_free
func pyroFree(ptr, _ uint32) {
	delete(pinned, ptr)
}

//go:wasmexport pyro_invoke
func pyroInvoke(ptr, size uint32) uint64 {
	// ptr is an address in this module's linear memory, from pyro_alloc.
	in := unsafe.Slice((*byte)(unsafe.Add(nil, int(ptr))), size)
	out := dispatch(in)
	delete(pinned, ptr)
	p := ptrOf(out)
	pinned[p] = out
	return uint64(p)<<32 | uint64(len(out))
}

//go:wasmimport pyro log
func hostLog(level uint32, ptr unsafe.Pointer, size uint32)

//go:wasmimport pyro net_dial
func hostDial(addr unsafe.Pointer, addrLen uint32, useTLS uint32, timeoutMS uint32) int32

//go:wasmimport pyro net_write
func hostWrite(handle int32, ptr unsafe.Pointer, size uint32) int32

//go:wasmimport pyro net_read
func hostRead(handle int32, ptr unsafe.Pointer, size uint32, timeoutMS uint32) int32

//go:wasmimport pyro net_close
func hostClose(handle int32)

//go:wasmimport pyro last_error
func hostLastError(ptr unsafe.Pointer, size uint32) uint32

func bytesPtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(unsafe.SliceData(b))
}
