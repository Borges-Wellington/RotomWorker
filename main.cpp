// main.cpp
// RotomWorker C++ (consolidado) - control + data + scanner + mitm handler + optional .so hooks
//
// Dependências:
//  - protobuf (c++ headers + lib) -> proto_gen/rotom.pb.h (gerado com protoc a partir de rotom.proto)
//  - websocketpp (+ asio standalone) -- for WebSocket client
//  - nlohmann/json
//  - pthread, dl (linux/android), etc.
//
// Como compilar (exemplo desktop Linux):
//  g++ -std=c++17 main.cpp proto_gen/rotom.pb.cc -Iproto_gen -I/path/to/asio/include -I/path/to/websocketpp -I/path/to/nlohmann -lprotobuf -lpthread -ldl -o rotom_worker
//
// Para compilar para Android NDK toolchain (exemplo):
//  export NDK=/path/to/android-ndk
//  export TOOLCHAIN=$NDK/toolchains/llvm/prebuilt/linux-x86_64
//  export CXX=$TOOLCHAIN/bin/aarch64-linux-android21-clang++
//  $CXX -std=c++17 --sysroot=$TOOLCHAIN/sysroot main.cpp proto_gen/rotom.pb.cc \
//    -Iproto_gen -I/path/to/asio -I/path/to/websocketpp -I/path/to/nlohmann \
//    -L/path/to/protobuf/lib -lprotobuf -ldl -lpthread -o rotom_worker_arm64
//
// Nota: você precisará ajustar caminhos para asio/websocketpp/nlohmann/protobuf conforme ambiente.
//

#include <iostream>
#include <fstream>
#include <sstream>
#include <string>
#include <thread>
#include <chrono>
#include <atomic>
#include <mutex>
#include <condition_variable>
#include <vector>
#include <deque>
#include <map>
#include <functional>
#include <filesystem>
#include <dlfcn.h> // dlopen
#include <memory>
#include <cstdlib>

#include "proto_gen/rotom.pb.h" // gerado a partir do rotom.proto

// third-party
#include "json.hpp"

// websocketpp + asio (standalone)
#include <websocketpp/config/asio_no_tls_client.hpp>
#include <websocketpp/client.hpp>

using json = nlohmann::json;
namespace fs = std::filesystem;

typedef websocketpp::client<websocketpp::config::asio_client> ws_client;
using connection_hdl = websocketpp::connection_hdl;

////////////////////////////////////////////////////////////////////////////////
// Config structure
////////////////////////////////////////////////////////////////////////////////
struct Config {
    struct Rotom {
        std::string worker_endpoint = "ws://127.0.0.1:9001";
        std::string device_endpoint = "";
        std::string secret = "";
        bool use_compression = false;
    } rotom;

    struct General {
        std::string device_name = "android-device";
        int workers = 1;
        std::string dns_server;
        std::string scan_dir = "/data/local/tmp/rotom_inbox";
    } general;

    struct Log {
        std::string level = "info";
    } log;

    int worker_spawn_delay_ms = 500;
};

Config readConfig(const std::string &path) {
    Config cfg;
    try {
        std::ifstream f(path);
        if (!f.is_open()) {
            std::cerr << "[config] warning: cannot open " << path << ", using defaults\n";
            return cfg;
        }
        json j;
        f >> j;
        if (j.contains("rotom")) {
            auto r = j["rotom"];
            if (r.contains("worker_endpoint")) cfg.rotom.worker_endpoint = r["worker_endpoint"].get<std::string>();
            if (r.contains("device_endpoint")) cfg.rotom.device_endpoint = r["device_endpoint"].get<std::string>();
            if (r.contains("secret")) cfg.rotom.secret = r["secret"].get<std::string>();
            if (r.contains("use_compression")) cfg.rotom.use_compression = r["use_compression"].get<bool>();
        }
        if (j.contains("general")) {
            auto g = j["general"];
            if (g.contains("device_name")) cfg.general.device_name = g["device_name"].get<std::string>();
            if (g.contains("workers")) cfg.general.workers = g["workers"].get<int>();
            if (g.contains("dns_server")) cfg.general.dns_server = g["dns_server"].get<std::string>();
            if (g.contains("scan_dir")) cfg.general.scan_dir = g["scan_dir"].get<std::string>();
        }
        if (j.contains("tuning")) {
            auto t = j["tuning"];
            if (t.contains("worker_spawn_delay_ms")) cfg.worker_spawn_delay_ms = t["worker_spawn_delay_ms"].get<int>();
        }
    } catch (const std::exception &ex) {
        std::cerr << "[config] parse error: " << ex.what() << " — using defaults\n";
    }
    return cfg;
}

////////////////////////////////////////////////////////////////////////////////
// Send queue
////////////////////////////////////////////////////////////////////////////////
struct SendItem {
    std::string path;
    std::vector<uint8_t> payload;
};

class SendQueue {
    std::deque<SendItem> q_;
    std::mutex m_;
    std::condition_variable cv_;
    bool closed_ = false;
public:
    void push(SendItem &&it) {
        {
            std::lock_guard<std::mutex> lk(m_);
            q_.push_back(std::move(it));
        }
        cv_.notify_one();
    }
    bool wait_pop(SendItem &out) {
        std::unique_lock<std::mutex> lk(m_);
        cv_.wait(lk, [&]{ return !q_.empty() || closed_; });
        if (!q_.empty()) {
            out = std::move(q_.front());
            q_.pop_front();
            return true;
        }
        return false;
    }
    void close() {
        {
            std::lock_guard<std::mutex> lk(m_);
            closed_ = true;
        }
        cv_.notify_all();
    }
    size_t size() {
        std::lock_guard<std::mutex> lk(m_);
        return q_.size();
    }
};

static SendQueue g_sendQueue;

////////////////////////////////////////////////////////////////////////////////
// Optional dynamic hook library (.so) integration
//
// We assume the .so (if provided) exports C functions with this signature:
//   extern "C" int HandleRequest(const uint8_t* in, size_t in_len, uint8_t** out, size_t* out_len);
//   extern "C" int HandleResponse(const uint8_t* in, size_t in_len, uint8_t** out, size_t* out_len);
//
// The function should allocate *out with malloc (or new[] compatible with free) and set out_len.
// Return 0 on success, non-zero on error.
//
// If your libs use different signatures, edit the typedefs below.
//
////////////////////////////////////////////////////////////////////////////////
typedef int (*so_HandleReq_t)(const uint8_t*, size_t, uint8_t**, size_t*);
typedef int (*so_HandleResp_t)(const uint8_t*, size_t, uint8_t**, size_t*);

struct HookLib {
    void *dl = nullptr;
    so_HandleReq_t handleReq = nullptr;
    so_HandleResp_t handleResp = nullptr;
    std::string path;
    bool loaded = false;

    bool load(const std::string &so_path) {
        path = so_path;
        dl = dlopen(so_path.c_str(), RTLD_NOW);
        if (!dl) {
            std::cerr << "[hook] dlopen("<<so_path<<") failed: " << dlerror() << "\n";
            return false;
        }
        handleReq = (so_HandleReq_t)dlsym(dl, "HandleRequest");
        handleResp = (so_HandleResp_t)dlsym(dl, "HandleResponse");
        // both are optional — we can have one or other
        loaded = true;
        std::cout << "[hook] loaded " << so_path << " handleReq=" << (handleReq!=nullptr) << " handleResp=" << (handleResp!=nullptr) << "\n";
        return true;
    }
    ~HookLib() {
        if (dl) dlclose(dl);
    }
};

static std::vector<std::unique_ptr<HookLib>> g_hooklibs;

////////////////////////////////////////////////////////////////////////////////
// WebSocket data client (simplified wrapper around websocketpp)
////////////////////////////////////////////////////////////////////////////////
struct WsDataClient {
    ws_client client;
    connection_hdl hdl;
    std::shared_ptr<ws_client::connection_type> con;
    std::mutex m;
    bool connected = false;

    WsDataClient() {
        client.init_asio();
        client.clear_access_channels(websocketpp::log::alevel::all);
        client.clear_error_channels(websocketpp::log::elevel::all);
    }

    void run_loop() {
        try {
            client.run();
        } catch (const std::exception &e) {
            std::cerr << "[ws] run exception: " << e.what() << "\n";
        }
    }

    bool connect(const std::string &uri, const std::string &secret) {
        websocketpp::lib::error_code ec;
        auto con_ptr = client.get_connection(uri, ec);
        if (ec) {
            std::cerr << "[ws] get_connection error: " << ec.message() << "\n";
            return false;
        }
        if (!secret.empty()) con_ptr->append_header("Authorization", "Bearer " + secret);

        con_ptr->set_open_handler([this](connection_hdl h) {
            std::lock_guard<std::mutex> lk(this->m);
            this->hdl = h;
            this->connected = true;
            std::cout << "[data] ws connected\n";
        });

        con_ptr->set_close_handler([this](connection_hdl) {
            std::lock_guard<std::mutex> lk(this->m);
            this->connected = false;
            std::cout << "[data] ws closed\n";
        });

        con_ptr->set_fail_handler([this](connection_hdl) {
            std::lock_guard<std::mutex> lk(this->m);
            this->connected = false;
            std::cout << "[data] ws fail\n";
        });

        con_ptr->set_message_handler([this](connection_hdl, ws_client::message_ptr msg) {
            if (msg->get_opcode() == websocketpp::frame::opcode::binary) {
                std::cout << "[data] recv binary size=" << msg->get_payload().size() << "\n";
                // Optionally parse incoming MitmResponse here.
            } else {
                std::cout << "[data] recv text: " << msg->get_payload() << "\n";
            }
        });

        this->con = con_ptr;
        client.connect(con_ptr);
        return true;
    }

    bool is_connected() {
        std::lock_guard<std::mutex> lk(m);
        return connected;
    }

    bool send_binary(const std::vector<uint8_t> &data) {
        std::lock_guard<std::mutex> lk(m);
        if (!connected) return false;
        websocketpp::lib::error_code ec;
        client.send(hdl, (const void*)data.data(), data.size(), websocketpp::frame::opcode::binary, ec);
        if (ec) {
            std::cerr << "[data] send error: " << ec.message() << "\n";
            return false;
        }
        return true;
    }

    bool send_text(const std::string &txt) {
        std::lock_guard<std::mutex> lk(m);
        if (!connected) return false;
        websocketpp::lib::error_code ec;
        client.send(hdl, txt, websocketpp::frame::opcode::text, ec);
        if (ec) {
            std::cerr << "[data] send text error: " << ec.message() << "\n";
            return false;
        }
        return true;
    }

    void stop() {
        try { client.stop(); } catch(...) {}
    }
};

static WsDataClient g_dataWs;

////////////////////////////////////////////////////////////////////////////////
// MITM handlers (maps like in CoffeeScript)
////////////////////////////////////////////////////////////////////////////////
using ReqHandlerFn = std::function<void(const RotomProtos::MitmRequest&, RotomProtos::MitmResponse&)>;
using RespHandlerFn = std::function<void(const RotomProtos::MitmResponse&)>;

static std::map<std::string, ReqHandlerFn> g_requestHandlers;
static std::map<std::string, RespHandlerFn> g_responseHandlers;

void addRequestHandler(const std::string &name, ReqHandlerFn cb) {
    g_requestHandlers[name] = cb;
}
void addResponseHandler(const std::string &name, RespHandlerFn cb) {
    g_responseHandlers[name] = cb;
}

////////////////////////////////////////////////////////////////////////////////
// handleRequestBuffer / handleResponseBuffer
//
// - Primeiro tenta passar pelo hook libs (if present); se lib retornar um buffer, usa-o.
// - Caso contrário, decodifica Protobuf MitmRequest/MitmResponse e chama handlers registrados.
////////////////////////////////////////////////////////////////////////////////
void handleRequestBuffer(const std::vector<uint8_t> &raw) {
    // 1) try hook libs
    for (auto &h : g_hooklibs) {
        if (h->handleReq) {
            uint8_t *out = nullptr;
            size_t out_len = 0;
            int rc = h->handleReq(raw.data(), raw.size(), &out, &out_len);
            if (rc == 0 && out && out_len > 0) {
                // send resulting buffer to rotom
                std::vector<uint8_t> v(out, out + out_len);
                free(out); // assume malloc'd in lib
                if (!g_dataWs.send_binary(v)) {
                    std::cerr << "[mitm] hook produced result but data WS not connected\n";
                }
                return;
            }
        }
    }

    // 2) decode with protobuf
    RotomProtos::MitmRequest req;
    if (!req.ParseFromArray(raw.data(), (int)raw.size())) {
        std::cerr << "[mitm] failed to parse MitmRequest\n";
        return;
    }

    // build an empty response object to be possibly filled by request handlers
    RotomProtos::MitmResponse resp;
    // example dispatch by method enum name
    std::string methodName = "UNSET";
    switch (req.method()) {
        case RotomProtos::MitmRequest::LOGIN: methodName = "LOGIN"; break;
        case RotomProtos::MitmRequest::RPC_REQUEST: methodName = "RPC_REQUEST"; break;
        default: methodName = "UNSET"; break;
    }

    auto it = g_requestHandlers.find(methodName);
    if (it != g_requestHandlers.end()) {
        it->second(req, resp);
    } else {
        // default: forward incoming MitmRequest to rotom as-is
        std::string s;
        req.SerializeToString(&s);
        std::vector<uint8_t> v(s.begin(), s.end());
        if (!g_dataWs.send_binary(v)) {
            std::cerr << "[mitm] failed send to rotom (data WS not connected)\n";
        }
        return;
    }

    // if handler set payload in resp, send it
    if (resp.status() != RotomProtos::MitmResponse::UNSET) {
        std::string s;
        resp.SerializeToString(&s);
        std::vector<uint8_t> v(s.begin(), s.end());
        if (!g_dataWs.send_binary(v)) {
            std::cerr << "[mitm] failed to send response to rotom\n";
        }
    }
}

void handleResponseBuffer(const std::vector<uint8_t> &raw) {
    // hook libs first
    for (auto &h : g_hooklibs) {
        if (h->handleResp) {
            uint8_t *out = nullptr;
            size_t out_len = 0;
            int rc = h->handleResp(raw.data(), raw.size(), &out, &out_len);
            if (rc == 0 && out && out_len > 0) {
                std::vector<uint8_t> v(out, out + out_len);
                free(out);
                // we can process or forward to Rotom; here forwarding:
                if (!g_dataWs.send_binary(v)) {
                    std::cerr << "[mitm] hook produced response but data WS not connected\n";
                }
                return;
            }
        }
    }

    RotomProtos::MitmResponse resp;
    if (!resp.ParseFromArray(raw.data(), (int)raw.size())) {
        std::cerr << "[mitm] failed to parse MitmResponse\n";
        return;
    }

    // dispatch by status / or RPC payload etc
    std::string key = std::to_string((int)resp.status());
    auto it = g_responseHandlers.find(key);
    if (it != g_responseHandlers.end()) {
        it->second(resp);
    } else {
        // default: just forward
        std::string s;
        resp.SerializeToString(&s);
        std::vector<uint8_t> v(s.begin(), s.end());
        if (!g_dataWs.send_binary(v)) {
            std::cerr << "[mitm] failed to send response to rotom\n";
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Scanner loop: lê arquivos do scanDir e enfileira para envio
////////////////////////////////////////////////////////////////////////////////
std::atomic<bool> g_running(true);

void scanDirLoop(const std::string &scanDir, int minSize = 512) {
    fs::path dir(scanDir);
    if (!fs::exists(dir)) {
        try { fs::create_directories(dir); } catch (...) {}
    }
    while (g_running.load()) {
        try {
            for (auto &p : fs::directory_iterator(dir)) {
                if (!fs::is_regular_file(p.path())) continue;
                auto sz = fs::file_size(p.path());
                if ((int)sz < minSize) continue;
                // read file
                std::ifstream ifs(p.path(), std::ios::binary);
                if (!ifs.is_open()) continue;
                std::vector<uint8_t> buf((std::istreambuf_iterator<char>(ifs)), std::istreambuf_iterator<char>());
                ifs.close();
                // enqueue
                SendItem it;
                it.path = p.path().string();
                it.payload = std::move(buf);
                g_sendQueue.push(std::move(it));
                std::cout << "[scan] enqueued " << it.path << "\n";
            }
        } catch (const std::exception &ex) {
            std::cerr << "[scan] error: " << ex.what() << "\n";
        }
        for (int i=0;i<15 && g_running.load();++i) std::this_thread::sleep_for(std::chrono::seconds(1));
    }
}



////////////////////////////////////////////////////////////////////////////////
// sender worker: pega da fila e envia ao rotom (binary). Remove arquivo ao enviar com sucesso.
////////////////////////////////////////////////////////////////////////////////
void senderWorker(int idx) {
    std::cout << "[worker" << idx << "] started\n";
    while (g_running.load()) {
        SendItem it;
        bool ok = g_sendQueue.wait_pop(it);
        if (!ok) break;
        if (!g_dataWs.is_connected()) {
            // requeue and sleep
            std::cerr << "[worker" << idx << "] data WS not connected; requeueing\n";
            g_sendQueue.push(std::move(it));
            std::this_thread::sleep_for(std::chrono::seconds(1));
            continue;
        }
        bool sent = g_dataWs.send_binary(it.payload);
        if (!sent) {
            std::cerr << "[worker" << idx << "] send failed, requeue\n";
            g_sendQueue.push(std::move(it));
            std::this_thread::sleep_for(std::chrono::seconds(1));
            continue;
        } else {
            std::cout << "[worker" << idx << "] sent " << it.path << " (" << it.payload.size() << " bytes)\n";
            try { fs::remove(it.path); } catch(...) {}
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(120));
    }
    std::cout << "[worker" << idx << "] exiting\n";
}

////////////////////////////////////////////////////////////////////////////////
// Control loop: connect to /control and periodically send heartbeat
// simplified: we use websocketpp in a local client instance and send text JSON
////////////////////////////////////////////////////////////////////////////////
void controlLoop(const Config &cfg) {
    ws_client c;
    c.init_asio();
    c.clear_access_channels(websocketpp::log::alevel::all);
    c.clear_error_channels(websocketpp::log::elevel::all);

    std::string uri = cfg.rotom.worker_endpoint + "/control";
    websocketpp::lib::error_code ec;
    auto con = c.get_connection(uri, ec);
    if (ec) {
        std::cerr << "[control] get_connection error: " << ec.message() << "\n";
        return;
    }
    if (!cfg.rotom.secret.empty()) con->append_header("Authorization", "Bearer " + cfg.rotom.secret);

    con->set_open_handler([&c,&cfg](connection_hdl h) {
        json intro = {
            {"deviceId", cfg.general.device_name},
            {"version", 1},
            {"origin", "lab"},
            {"publicIp", "127.0.0.1"},
            {"secret", cfg.rotom.secret}
        };
        websocketpp::lib::error_code ec2;
        c.send(h, intro.dump(), websocketpp::frame::opcode::text, ec2);
        if (ec2) std::cerr << "[control] send intro error: " << ec2.message() << "\n";
        std::cout << "[control] intro sent\n";
    });

    con->set_message_handler([&](connection_hdl, ws_client::message_ptr msg) {
        std::cout << "[control] recv: " << msg->get_payload() << "\n";
    });

    c.connect(con);

    std::thread t([&c](){ c.run(); });

    while (g_running.load()) {
        std::this_thread::sleep_for(std::chrono::seconds(15));
        json hb = {
            {"type", "heartbeat"},
            {"ts", (int64_t)std::chrono::duration_cast<std::chrono::seconds>(
                std::chrono::system_clock::now().time_since_epoch()).count()},
            {"workerId", cfg.general.device_name}
        };
        // We can't easily access connection handle here without more plumbing; print heartbeat
        std::cout << "[control] heartbeat: " << hb.dump() << "\n";
    }

    c.stop();
    if (t.joinable()) t.join();
}

////////////////////////////////////////////////////////////////////////////////
// small helper: send WelcomeMessage as protobuf over data WS
////////////////////////////////////////////////////////////////////////////////
bool sendWelcome(const Config &cfg) {
    RotomProtos::WelcomeMessage w;
    w.set_worker_id(cfg.general.device_name);
    w.set_origin("lab");
    w.set_version_code(1);
    w.set_version_name("rotom-worker-cpp");
    w.set_useragent("rotom-worker-cpp/1.0");
    w.set_device_id(cfg.general.device_name + "-device");
    std::string s;
    if (!w.SerializeToString(&s)) return false;
    std::vector<uint8_t> v(s.begin(), s.end());
    return g_dataWs.send_binary(v);
}

////////////////////////////////////////////////////////////////////////////////
// Cosmog-style: Load ART and Niantic Plugin hooks dynamically
////////////////////////////////////////////////////////////////////////////////
struct CosmogHooks {
    void* artHandle = nullptr;
    void* nianticHandle = nullptr;
    void (*handleRequest)(void*, size_t) = nullptr;
    void (*handleResponse)(void*, size_t) = nullptr;
    void (*pluginInit)() = nullptr;

    bool load_art() {
        const char* path = "/system/lib64/libart.so";
        artHandle = dlopen(path, RTLD_NOW);
        if (!artHandle) {
            std::cerr << "[cosmog] ❌ Falha ao carregar libart.so: " << dlerror() << "\n";
            return false;
        }
        std::cout << "[cosmog] ✅ libart.so carregada.\n";
        return true;
    }

    bool load_niantic() {
        const char* path = "/data/local/tmp/libNianticLabsPlugin.so";
        nianticHandle = dlopen(path, RTLD_NOW);
        if (!nianticHandle) {
            std::cerr << "[cosmog] ❌ Falha ao carregar libNianticLabsPlugin.so: " << dlerror() << "\n";
            return false;
        }
        std::cout << "[cosmog] ✅ libNianticLabsPlugin.so carregada.\n";

        handleRequest = reinterpret_cast<void(*)(void*,size_t)>(dlsym(nianticHandle, "handleRequest"));
        handleResponse = reinterpret_cast<void(*)(void*,size_t)>(dlsym(nianticHandle, "handleResponse"));
        pluginInit = reinterpret_cast<void(*)()>(dlsym(nianticHandle, "PluginInit"));

        std::cout << "[cosmog] handleRequest: " << (handleRequest ? "✅ encontrado" : "⚠️ não encontrado") << "\n";
        std::cout << "[cosmog] handleResponse: " << (handleResponse ? "✅ encontrado" : "⚠️ não encontrado") << "\n";
        if (pluginInit) {
            std::cout << "[cosmog] Chamando PluginInit()...\n";
            pluginInit();
        }

        return true;
    }

    void unload() {
        if (nianticHandle) {
            dlclose(nianticHandle);
            nianticHandle = nullptr;
            std::cout << "[cosmog] 🔻 libNianticLabsPlugin.so descarregada.\n";
        }
        if (artHandle) {
            dlclose(artHandle);
            artHandle = nullptr;
            std::cout << "[cosmog] 🔻 libart.so descarregada.\n";
        }
    }
};

static CosmogHooks g_cosmog;

void load_cosmog_libs() {
    std::cout << "[cosmog] 🚀 Inicializando Cosmog hook loader...\n";
    g_cosmog.load_art();
    g_cosmog.load_niantic();
}


////////////////////////////////////////////////////////////////////////////////
// Entrypoint
////////////////////////////////////////////////////////////////////////////////
int main(int argc, char** argv) {
    std::string cfgPath = "/data/local/tmp/rotom-config.json";
    if (argc > 1) cfgPath = argv[1];

    Config cfg = readConfig(cfgPath);
    std::cout << "rotom-worker (C++) starting; rotom=" << cfg.rotom.worker_endpoint << " scanDir=" << cfg.general.scan_dir << "\n";

    load_cosmog_libs();
    // Load optional hook libs via environment variable ROTOM_HOOKS (colon separated) or fixed paths
    const char* hooksEnv = std::getenv("ROTOM_HOOKS");
    if (hooksEnv) {
        std::string s(hooksEnv);
        std::stringstream ss(s);
        std::string token;
        while (std::getline(ss, token, ':')) {
            if (token.empty()) continue;
            auto hl = std::make_unique<HookLib>();
            if (hl->load(token)) {
                g_hooklibs.push_back(std::move(hl));
            } else {
                std::cerr << "[main] failed to load hook: " << token << "\n";
            }
        }
    }

    // Register a couple of example handlers (you can expand these similarly to your CoffeeScript hooks)
    addRequestHandler("LOGIN", [](const RotomProtos::MitmRequest &req, RotomProtos::MitmResponse &resp) {
        std::cout << "[handler] LOGIN intercepted (worker_id=" << req.login_request().worker_id() << ")\n";
        // respond success example
        resp.set_status(RotomProtos::MitmResponse::SUCCESS);
        RotomProtos::MitmResponse::LoginResponse *lr = resp.mutable_login_response();
        lr->set_worker_id(req.login_request().worker_id());
        lr->set_status(RotomProtos::AUTH_STATUS_GOT_AUTH_TOKEN);
        lr->set_supports_compression(false);
        lr->set_useragent("rotom-worker-cpp/1.0");
    });

    addRequestHandler("RPC_REQUEST", [](const RotomProtos::MitmRequest &req, RotomProtos::MitmResponse &resp) {
        std::cout << "[handler] RPC_REQUEST with " << req.rpc_request().request_size() << " inner requests\n";
        // default: forward unchanged (we'll build a "rpc_response" wrapper)
        resp.set_status(RotomProtos::MitmResponse::SUCCESS);
        RotomProtos::MitmResponse::RpcResponse *rr = resp.mutable_rpc_response();
        rr->set_rpc_status(RotomProtos::RPC_STATUS_SUCCESS);
        // not filling inner responses here — real usage likely wants to proxy the RPC inner payloads
    });

    // Start control thread
    std::thread ctlThread([&cfg](){ controlLoop(cfg); });

    // Start data websocket runloop in a separate thread
    std::thread wsRunThread([&cfg](){
        // run websocketpp internal loop in different thread; we connect in a connector thread
        g_dataWs.run_loop();
    });

    // Connector thread: attempt to connect and send WelcomeMessage
    std::thread connector([&cfg](){
        int backoff = 1;
        while (g_running.load()) {
            if (!g_dataWs.is_connected()) {
                std::cout << "[data] attempting connect to " << cfg.rotom.worker_endpoint << " ...\n";
                bool ok = g_dataWs.connect(cfg.rotom.worker_endpoint + "/", cfg.rotom.secret);
                if (!ok) {
                    std::cerr << "[data] connect failed, retrying in " << backoff << "s\n";
                    std::this_thread::sleep_for(std::chrono::seconds(backoff));
                    if (backoff < 30) backoff *= 2;
                    continue;
                } else {
                    std::cout << "[data] connected to data endpoint\n";
                    if (!sendWelcome(cfg)) {
                        std::cerr << "[data] failed to send WelcomeMessage protobuf\n";
                    } else {
                        std::cout << "[data] WelcomeMessage protobuf sent\n";
                    }
                }
            }
            std::this_thread::sleep_for(std::chrono::seconds(2));
        }
    });

    // start scanner thread
    std::thread scanner([&cfg](){ scanDirLoop(cfg.general.scan_dir); });

    // spawn worker threads
    std::vector<std::thread> workers;
    int workerCount = std::max(1, cfg.general.workers);
    for (int i=0;i<workerCount;++i) {
        workers.emplace_back(std::thread(senderWorker, i+1));
        std::this_thread::sleep_for(std::chrono::milliseconds(cfg.worker_spawn_delay_ms));
    }

    // join on signal (simple)
    std::cout << "[main] running — press Ctrl+C to stop\n";
    // wait until SIGINT/Ctrl+C
    std::atomic<bool> stop(false);
    std::signal(SIGINT, [](int){ g_running.store(false); });
    std::signal(SIGTERM, [](int){ g_running.store(false); });

    while (g_running.load()) std::this_thread::sleep_for(std::chrono::seconds(1));

    // shutdown
    std::cout << "[main] shutting down\n";
    g_sendQueue.close();
    if (scanner.joinable()) scanner.join();
    for (auto &t : workers) if (t.joinable()) t.join();
    if (connector.joinable()) connector.join();
    // stop ws
    g_dataWs.stop();
    if (wsRunThread.joinable()) wsRunThread.join();
    if (ctlThread.joinable()) ctlThread.join();

    std::cout << "rotom-worker stopped\n";
    return 0;
}
