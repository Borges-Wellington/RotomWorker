// niantic_hooks_package.cpp
#include <dlfcn.h>
#include <dirent.h>
#include <sys/stat.h>
#include <unistd.h>

#include <cstring>
#include <iostream>
#include <string>
#include <vector>
#include <functional>

static void* g_handle = nullptr;
static void (*g_pluginInit)() = nullptr;
static int (*g_handleRequest)(const unsigned char*, size_t, unsigned char**, size_t*) = nullptr;
static int (*g_handleResponse)(const unsigned char*, size_t, unsigned char**, size_t*) = nullptr;

static bool file_exists(const std::string& path) {
    struct stat st;
    return (stat(path.c_str(), &st) == 0);
}

// retorna candidatos construídos a partir do package name
static std::vector<std::string> make_candidates_for_package(const std::string& pkg) {
    std::vector<std::string> c;
    // direct local tmp (for test pushes)
    c.push_back("/data/local/tmp/lib/libNianticLabsPlugin.so");
    c.push_back("/data/local/tmp/libNianticLabsPlugin.so");

    // common data folder (older patterns)
    c.push_back("/data/data/" + pkg + "/lib/libNianticLabsPlugin.so");
    c.push_back("/data/data/" + pkg + "/lib64/libNianticLabsPlugin.so");

    // /data/app/<pkg>-*/lib/arm64  and variants
    const char* base = "/data/app";
    DIR* d = opendir(base);
    if (d) {
        struct dirent* ent;
        while ((ent = readdir(d)) != nullptr) {
            if (ent->d_name[0] == '.') continue;
            std::string appdir = std::string(base) + "/" + ent->d_name;
            // try many likely subpaths
            std::vector<std::string> tries = {
                appdir + "/lib/arm64/libNianticLabsPlugin.so",
                appdir + "/lib/arm64-v8a/libNianticLabsPlugin.so",
                appdir + "/lib64/libNianticLabsPlugin.so",
                appdir + "/lib/arm64/lib/" // placeholder
            };
            for (auto &tp : tries) {
                // if the entry contains the package name, prefer it
                if (appdir.find(pkg) != std::string::npos) {
                    c.push_back(tp);
                } else {
                    // still push in case package dir uses suffixes
                    c.push_back(tp);
                }
            }
        }
        closedir(d);
    }

    return c;
}

extern "C" {

// InitNianticHooksForPackage tenta localizar e carregar a lib no contexto do packageName.
// packageName ex: "com.nianticlabs.pokemongo"
int InitNianticHooksForPackage(const char* packageName) {
    if (g_handle != nullptr) {
        std::cerr << "[niantic_hooks] already initialized\n";
        return 0;
    }
    if (!packageName) {
        std::cerr << "[niantic_hooks] packageName == NULL\n";
        return 2;
    }

    std::string pkg(packageName);
    auto candidates = make_candidates_for_package(pkg);

    std::string chosen;
    for (const auto &p : candidates) {
        if (p.empty()) continue;
        if (file_exists(p)) {
            chosen = p;
            break;
        }
    }

    if (chosen.empty()) {
        std::cerr << "[niantic_hooks] did not find plugin for package: " << pkg << "\n";
        return 3;
    }

    g_handle = dlopen(chosen.c_str(), RTLD_NOW);
    if (!g_handle) {
        std::cerr << "[niantic_hooks] dlopen failed for " << chosen << ": " << dlerror() << "\n";
        return 4;
    }

    std::cerr << "[niantic_hooks] loaded " << chosen << "\n";

    // lookup symbols
    g_pluginInit = (void(*)())dlsym(g_handle, "PluginInit");
    g_handleRequest = (int(*)(const unsigned char*, size_t, unsigned char**, size_t*))dlsym(g_handle, "HandleRequest");
    g_handleResponse = (int(*)(const unsigned char*, size_t, unsigned char**, size_t*))dlsym(g_handle, "HandleResponse");

    std::cerr << "[niantic_hooks] PluginInit: " << (g_pluginInit ? "found" : "not found")
              << ", HandleRequest: " << (g_handleRequest ? "found" : "not found")
              << ", HandleResponse: " << (g_handleResponse ? "found" : "not found")
              << "\n";

    if (g_pluginInit) {
        g_pluginInit();
        std::cerr << "[niantic_hooks] PluginInit() called\n";
    }

    return 0;
}

// wrappers
int Niantic_HandleRequest(const unsigned char* in, size_t in_len, unsigned char** out, size_t* out_len) {
    if (!g_handleRequest) return -1;
    return g_handleRequest(in, in_len, out, out_len);
}

int Niantic_HandleResponse(const unsigned char* in, size_t in_len, unsigned char** out, size_t* out_len) {
    if (!g_handleResponse) return -1;
    return g_handleResponse(in, in_len, out, out_len);
}

void Niantic_Unload() {
    if (g_handle) {
        dlclose(g_handle);
        g_handle = nullptr;
        g_pluginInit = nullptr;
        g_handleRequest = nullptr;
        g_handleResponse = nullptr;
        std::cerr << "[niantic_hooks] unloaded\n";
    }
}

} // extern "C"
