package main

/*
#include "bridge.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"sync"
	"unsafe"
)

var lifecycle sync.RWMutex
var dispatcher abi.Dispatcher
var bridge *abi.Bridge

func main() {}

//export newapiPluginInit
func newapiPluginInit(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || host.abi_version != 1 || host.call == nil || host.free_buffer == nil {
		return 1
	}
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if dispatcher != nil {
		return 1
	}
	// Copy the instance callbacks. They are valid until all plugin work has stopped.
	h := *host
	bridge = abi.NewBridge(func(method string, b []byte) ([]byte, error) {
		m := C.CString(method)
		defer C.free(unsafe.Pointer(m))
		p := C.CBytes(b)
		defer C.free(p)
		var out C.cliproxy_buffer
		rc := C.invoke_host(&h, m, (*C.uint8_t)(p), C.size_t(len(b)), &out)
		if out.ptr != nil {
			defer C.release_host(&h, out.ptr, out.len)
		}
		if rc != 0 || out.ptr == nil || out.len > 32<<20 {
			return nil, errors.New("host callback failed")
		}
		return C.GoBytes(out.ptr, C.int(out.len)), nil
	})
	dispatcher = newDispatcher(bridge)
	C.set_plugin_api(plugin)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, n C.size_t, response *C.cliproxy_buffer) (rc C.int) {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	write := func(b []byte) { response.ptr = C.CBytes(b); response.len = C.size_t(len(b)) }
	defer func() {
		if recover() != nil {
			write(abi.Envelope(nil, errors.New("panic")))
			rc = 1
		}
	}()
	if method == nil || n > 2<<20 || (n > 0 && request == nil) {
		write(abi.Envelope(nil, errors.New("invalid request")))
		return 1
	}
	lifecycle.RLock()
	d := dispatcher
	lifecycle.RUnlock()
	if d == nil {
		write(abi.Envelope(nil, errors.New("uninitialized")))
		return 1
	}
	var b json.RawMessage
	if n > 0 {
		b = C.GoBytes(unsafe.Pointer(request), C.int(n))
	}
	out, err := d.Handle(context.Background(), C.GoString(method), b)
	write(abi.Envelope(out, err))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, n C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	if dispatcher != nil {
		_ = dispatcher.Close(context.Background())
		dispatcher = nil
	}
	if bridge != nil {
		bridge.Close()
		bridge = nil
	}
}
