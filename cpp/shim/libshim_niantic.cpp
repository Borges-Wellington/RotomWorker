// libshim_niantic.cpp
#include <dlfcn.h>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>
#include <sys/stat.h>
#include <dirent.h>

static void* real_handle = nullptr;

// pointers to real functions (if found)
typedef void (*real_init_t)();
typedef int (*real_handle_fn)(const unsigned char*, size_t, unsigned char**, size_t*);

static real_init_t real_PluginInit = nullptr;
static real_handle_fn real_HandleRequest = nullptr;
static real_handle_fn real_HandleResponse = nullptr;

static bool file_exists(const char* path) {
    struct stat st;
    return stat(path, &st) == 0;
}

static std::vector<std::string> default_candidates() {
    std::vector<std::string> c;
    // local tmp (where you'll likely push the real lib for tests)
    c.push_back("/data/local/tmp/lib/libNianticLabsPlugin.so");
    c.push_back("/data/local/tmp/libNianticLabsPlugin.so");
    // common app folders (best-effort)
    c.push_back("/data/data/com.nianticlabs.pokemongo/lib/libNianticLabsPlugin.so");
    c.push_back("/data/data/com.nianticlabs.pokemongo/lib64/libNianticLabsPlugin.so");
    // search under /data/app for matches (not exhaustive)
    const char* base = "/data/app";
    DIR* d = opendir(base);
    if (d) {
        struct dirent* ent;
        while ((ent = readdir(d)) != nullptr) {
            if (ent->d_name[0] == '.') continue;
            std::string appdir = std::string(base) + "/" + ent->d_name;
            c.push_back(appdir + "/lib/arm64/libNianticLabsPlugin.so");
            c.push_back(appdir + "/lib/arm64-v8a/libNianticLabsPlugin.so");
            c.push_back(appdir + "/lib64/libNianticLabsPlugin.so");
        }
        closedir(d);
    }
    return c;
}

extern "C" {

// Shim PluginInit: tenta localizar e chamar real PluginInit, senão faz noop.
void PluginInit() {
    // if already loaded, call real if available
    if (!real_handle) {
        // try environment var first
        const char* p = getenv("ROTOM_ORIG_LIB");
        std::string chosen;
        if (p && file_exists(p)) {
            chosen = p;
        } else {
            // try candidates
            auto cand = default_candidates();
            for (auto &cp : cand) {
                if (file_exists(cp.c_str())) {
                    chosen = cp;
                    break;
                }
            }
        }

        if (!chosen.empty()) {
            real_handle = dlopen(chosen.c_str(), RTLD_NOW);
            if (real_handle) {
                // try resolve symbols by plain names
                real_PluginInit = (real_init_t)dlsym(real_handle, "PluginInit");
                real_HandleRequest = (real_handle_fn)dlsym(real_handle, "HandleRequest");
                real_HandleResponse = (real_handle_fn)dlsym(real_handle, "HandleResponse");
                // log
                printf("[shim] loaded real lib: %s (init=%p req=%p resp=%p)\n",
                       chosen.c_str(),
                       (void*)real_PluginInit,
                       (void*)real_HandleRequest,
                       (void*)real_HandleResponse);
            } else {
                printf("[shim] dlopen failed for %s: %s\n", chosen.c_str(), dlerror());
            }
        } else {
            printf("[shim] no candidate real lib found; continuing with shim-only behavior\n");
        }
    }

    if (real_PluginInit) {
        real_PluginInit();
        printf("[shim] called real PluginInit()\n");
    } else {
        // shim fallback
        printf("[shim] PluginInit (shim noop)\n");
    }
}

// HandleRequest: tenta delegar ao real HandleRequest; se não existir, ecoa o buffer.
int HandleRequest(const unsigned char* in, size_t in_len, unsigned char** out, size_t* out_len) {
    if (!in || in_len == 0) {
        *out = nullptr;
        *out_len = 0;
        return 0;
    }

    if (real_HandleRequest) {
        int rc = real_HandleRequest(in, in_len, out, out_len);
        // if success and out provided, just return
        if (rc == 0 && *out != nullptr && *out_len > 0) {
            return 0;
        }
        // if real returns no out but success, return success with no out
        if (rc == 0) {
            *out = nullptr;
            *out_len = 0;
            return 0;
        }
        // if rc != 0, fallthrough to fallback behavior
    }

    // fallback: echo input (allocate with malloc)
    unsigned char* buf = (unsigned char*)malloc(in_len);
    if (!buf) {
        *out = nullptr;
        *out_len = 0;
        return -1;
    }
    memcpy(buf, in, in_len);
    *out = buf;
    *out_len = in_len;
    return 0;
}

// HandleResponse: delegate if real exists; otherwise no-op (out = NULL)
int HandleResponse(const unsigned char* in, size_t in_len, unsigned char** out, size_t* out_len) {
    if (!in || in_len == 0) {
        *out = nullptr;
        *out_len = 0;
        return 0;
    }

    if (real_HandleResponse) {
        int rc = real_HandleResponse(in, in_len, out, out_len);
        if (rc == 0 && *out != nullptr && *out_len > 0) {
            return 0;
        }
        if (rc == 0) {
            *out = nullptr;
            *out_len = 0;
            return 0;
        }
        // otherwise fallthrough
    }

    // fallback: do not transform response; set out = NULL meaning "no special output"
    *out = nullptr;
    *out_len = 0;
    return 0;
}

// Optional unload helper
void Shim_Unload() {
    if (real_handle) {
        dlclose(real_handle);
        real_handle = nullptr;
        real_PluginInit = nullptr;
        real_HandleRequest = nullptr;
        real_HandleResponse = nullptr;
        printf("[shim] real lib unloaded\n");
    }
}

} // extern "C"
