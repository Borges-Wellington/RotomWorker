package internal

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

// expected function signatures in user .so
typedef int (*so_HandleReq_t)(const uint8_t*, size_t, uint8_t**, size_t*);
typedef int (*so_HandleResp_t)(const uint8_t*, size_t, uint8_t**, size_t*);
typedef void (*so_PluginInit_t)();

static void* rw_load_library(const char* path) {
    return dlopen(path, RTLD_NOW | RTLD_LOCAL);
}

static void* rw_get_symbol(void* lib, const char* sym) {
    if (!lib) return NULL;
    return dlsym(lib, sym);
}

static int rw_call_handle_request(void* fn, const uint8_t* in, size_t in_len, uint8_t** out, size_t* out_len) {
    if (!fn) return -1;
    so_HandleReq_t f = (so_HandleReq_t)fn;
    return f(in, in_len, out, out_len);
}

static int rw_call_handle_response(void* fn, const uint8_t* in, size_t in_len, uint8_t** out, size_t* out_len) {
    if (!fn) return -1;
    so_HandleResp_t f = (so_HandleResp_t)fn;
    return f(in, in_len, out, out_len);
}

static int rw_call_plugin_init(void* fn) {
    if (!fn) return -1;
    so_PluginInit_t f = (so_PluginInit_t)fn;
    f();
    return 0;
}

static const char* rw_dlerror() {
    const char* e = dlerror();
    if (!e) return "";
    return e;
}
*/
import "C"

import (
    "errors"
    "fmt"
    "io/ioutil"
    "unsafe"

    "github.com/sirupsen/logrus"
)

type HookLib struct {
    Path        string
    lib         unsafe.Pointer
    handleReq   unsafe.Pointer
    handleResp  unsafe.Pointer
    pluginInit  unsafe.Pointer
    loaded      bool
    logger      *logrus.Logger
}

var LoadedHookLibs []*HookLib

func LoadHookLib(path string) error {
    lg := NewLogger()
    h := &HookLib{Path: path, logger: lg}
    // verify file exists quickly
    if b, err := ioutil.ReadFile(path); err != nil || len(b) < 4 {
        return fmt.Errorf("file not readable: %v", err)
    }
    cpath := C.CString(path)
    defer C.free(unsafe.Pointer(cpath))
    lib := C.rw_load_library(cpath)
    if lib == nil {
        errStr := C.GoString(C.rw_dlerror())
        return fmt.Errorf("dlopen failed for %s: %s", path, errStr)
    }
    h.lib = lib
    // try find symbols
    reqSym := C.CString("HandleRequest")
    respSym := C.CString("HandleResponse")
    initSym := C.CString("PluginInit")
    defer C.free(unsafe.Pointer(reqSym))
    defer C.free(unsafe.Pointer(respSym))
    defer C.free(unsafe.Pointer(initSym))

    rs := C.rw_get_symbol(h.lib, reqSym)
    if rs != nil {
        h.handleReq = rs
    }
    rr := C.rw_get_symbol(h.lib, respSym)
    if rr != nil {
        h.handleResp = rr
    }
    pi := C.rw_get_symbol(h.lib, initSym)
    if pi != nil {
        h.pluginInit = pi
        // call plugin init (best-effort)
        C.rw_call_plugin_init(pi)
        h.logger.Infof("called PluginInit() for %s", path)
    }

    h.loaded = true
    LoadedHookLibs = append(LoadedHookLibs, h)
    h.logger.Infof("loaded hook lib %s (req=%v resp=%v init=%v)", path, h.handleReq != nil, h.handleResp != nil, h.pluginInit != nil)
    return nil
}

// TryHandleRequest: calls HandleRequest on first lib that provides it.
// Returns (handled bool, out []byte, err)
func TryHandleRequest(in []byte) (bool, []byte, error) {
    for _, h := range LoadedHookLibs {
        if h.handleReq == nil {
            continue
        }
        var outptr *C.uint8_t
        var outlen C.size_t
        // call
        rc := C.rw_call_handle_request(h.handleReq, (*C.uint8_t)(unsafe.Pointer(&in[0])), C.size_t(len(in)), &outptr, &outlen)
        if rc == 0 && outptr != nil && outlen > 0 {
            outGo := C.GoBytes(unsafe.Pointer(outptr), C.int(outlen))
            // assume C side used malloc/new compatible with free
            C.free(unsafe.Pointer(outptr))
            return true, outGo, nil
        }
    }
    return false, nil, errors.New("no hook handled")
}

// TryHandleResponse: same for responses
func TryHandleResponse(in []byte) (bool, []byte, error) {
    for _, h := range LoadedHookLibs {
        if h.handleResp == nil {
            continue
        }
        var outptr *C.uint8_t
        var outlen C.size_t
        rc := C.rw_call_handle_response(h.handleResp, (*C.uint8_t)(unsafe.Pointer(&in[0])), C.size_t(len(in)), &outptr, &outlen)
        if rc == 0 && outptr != nil && outlen > 0 {
            outGo := C.GoBytes(unsafe.Pointer(outptr), C.int(outlen))
            C.free(unsafe.Pointer(outptr))
            return true, outGo, nil
        }
    }
    return false, nil, errors.New("no hook handled resp")
}
