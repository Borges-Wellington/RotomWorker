package internal

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

// Tipos de função exportados pelas libs ELF
typedef int (*handle_fn)(const unsigned char*, size_t, unsigned char**, size_t*);
typedef void (*init_fn)();

// --- Funções com linkagem externa (Go precisa enxergar essas) ---
void* load_lib(const char* path)               { return dlopen(path, RTLD_NOW); }
void* load_symbol(void* handle, const char* s) { return dlsym(handle, s); }
const char* last_dl_error()                    { return dlerror(); }

// Wrappers que o Go pode chamar
void call_init_fn(init_fn fn) {
    if (fn != NULL) fn();
}

int call_handle_fn(handle_fn fn,
                   const unsigned char* in, size_t in_len,
                   unsigned char** out, size_t* out_len) {
    if (fn == NULL) return -1;
    return fn(in, in_len, out, out_len);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Estrutura de controle de libs ELF carregadas
type ElfHook struct {
	handle         unsafe.Pointer
	handleRequest  unsafe.Pointer
	handleResponse unsafe.Pointer
	pluginInit     unsafe.Pointer
	path           string
}

// Lista de hooks carregados
var activeElfHooks []*ElfHook

// LoadElfHooks carrega as bibliotecas ELF e resolve os símbolos principais
func LoadElfHooks(paths []string) error {
	for _, path := range paths {
		cpath := C.CString(path)
		defer C.free(unsafe.Pointer(cpath))

		handle := C.load_lib(cpath)
		if handle == nil {
			return fmt.Errorf("dlopen failed for %s: %s", path, C.GoString(C.last_dl_error()))
		}

		hook := &ElfHook{
			handle: handle,
			path:   path,
		}

		// Carrega símbolos exportados
		creq := C.CString("HandleRequest")
		cresp := C.CString("HandleResponse")
		cinit := C.CString("PluginInit")
		defer C.free(unsafe.Pointer(creq))
		defer C.free(unsafe.Pointer(cresp))
		defer C.free(unsafe.Pointer(cinit))

		hook.handleRequest = C.load_symbol(handle, creq)
		hook.handleResponse = C.load_symbol(handle, cresp)
		hook.pluginInit = C.load_symbol(handle, cinit)

		activeElfHooks = append(activeElfHooks, hook)

		fmt.Printf("[elfhook] loaded %s (req=%v resp=%v init=%v)\n",
			path,
			hook.handleRequest != nil,
			hook.handleResponse != nil,
			hook.pluginInit != nil,
		)

		// Executa PluginInit se disponível
		if hook.pluginInit != nil {
			fn := (C.init_fn)(hook.pluginInit)
			C.call_init_fn(fn)
			fmt.Printf("[elfhook] PluginInit executed for %s\n", path)
		}
	}
	return nil
}

// TryProcessRequest envia um buffer para HandleRequest se a lib implementar
func TryProcessRequest(buf []byte) ([]byte, error) {
	for _, h := range activeElfHooks {
		if h.handleRequest != nil {
			var out *C.uchar
			var outLen C.size_t

			fn := (C.handle_fn)(h.handleRequest)
			rc := C.call_handle_fn(fn,
				(*C.uchar)(unsafe.Pointer(&buf[0])),
				C.size_t(len(buf)),
				&out,
				&outLen,
			)
			if rc == 0 && out != nil {
				defer C.free(unsafe.Pointer(out))
				goBuf := C.GoBytes(unsafe.Pointer(out), C.int(outLen))
				return goBuf, nil
			}
		}
	}
	return nil, errors.New("no HandleRequest active")
}

// TryProcessResponse envia um buffer para HandleResponse se a lib implementar
func TryProcessResponse(buf []byte) ([]byte, error) {
	for _, h := range activeElfHooks {
		if h.handleResponse != nil {
			var out *C.uchar
			var outLen C.size_t

			fn := (C.handle_fn)(h.handleResponse)
			rc := C.call_handle_fn(fn,
				(*C.uchar)(unsafe.Pointer(&buf[0])),
				C.size_t(len(buf)),
				&out,
				&outLen,
			)
			if rc == 0 && out != nil {
				defer C.free(unsafe.Pointer(out))
				goBuf := C.GoBytes(unsafe.Pointer(out), C.int(outLen))
				return goBuf, nil
			}
		}
	}
	return nil, errors.New("no HandleResponse active")
}
