package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static int invoke_host(cliproxy_host_api* h,const char* m,const uint8_t* b,size_t n,cliproxy_buffer* out) {return h->call(h->host_ctx,m,b,n,out);}
static void release_host(cliproxy_host_api* h,void* p,size_t n) {if(p)h->free_buffer(p,n);}
*/
import "C"

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"encoding/json"
	"errors"
	"sync"
	"unsafe"
)

var lifecycle sync.RWMutex
var dispatcher abi.Dispatcher
var bridge *abi.Bridge

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
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
	plugin.abi_version = 1
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
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
