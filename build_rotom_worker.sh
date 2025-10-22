#!/bin/bash
set -e

echo "[+] Compilando RotomWorker (Cosmog-style)..."

# Caminhos principais
NDK="$HOME/Android/Sdk/ndk/26.1.10909125"
TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/linux-x86_64"
CC="$TOOLCHAIN/bin/aarch64-linux-android21-clang++"

BASE_DIR="/home/user/Documentos/Wellington/Lab"
WORKER_DIR="$BASE_DIR/RotomWorker"
PROTOBUF_DIR="$BASE_DIR/protobuf/build_android/install"
ABSL_DIR="$BASE_DIR/protobuf/build_android/_deps/absl-build/absl"

PROTOBUF_INCLUDE="$PROTOBUF_DIR/include"
PROTOBUF_LIB="$PROTOBUF_DIR/lib"
INCLUDE_DIR="$WORKER_DIR/include"

# Caminho das libs opcionais (.so)
LIBS_DIR="$WORKER_DIR/lib"

# Compila objetos
echo "[+] Compilando objetos..."
$CC -std=c++17 --sysroot=$TOOLCHAIN/sysroot \
  -DASIO_STANDALONE -D_WEBSOCKETPP_CPP11_STL_ -D_WEBSOCKETPP_CPP11_THREAD_ -pthread \
  -I$INCLUDE_DIR -I$INCLUDE_DIR/asio -I$INCLUDE_DIR/websocketpp -I$INCLUDE_DIR/nlohmann \
  -I$PROTOBUF_INCLUDE -I$WORKER_DIR/proto_gen \
  -include asio.hpp \
  -c $WORKER_DIR/main.cpp $WORKER_DIR/proto_gen/rotom.pb.cc \
  -O2 -fPIE -fPIC

# Linka binário final
echo "[+] Linkando rotom_worker_arm64..."
$CC -o rotom_worker_arm64 main.o rotom.pb.o \
  -L$PROTOBUF_LIB \
  -L$ABSL_LIB/base -L$ABSL_LIB/container -L$ABSL_LIB/hash \
  -L$ABSL_LIB/log -L$ABSL_LIB/synchronization -L$ABSL_LIB/strings -L$ABSL_LIB/types -L$ABSL_LIB/utility \
  -L$ABSL_LIB/status -L$ABSL_LIB/time -L$ABSL_LIB/debugging \
  -l:libprotobuf.a \
  -labsl_base -labsl_malloc_internal -labsl_synchronization -labsl_strings -labsl_strings_internal \
  -labsl_raw_hash_set -labsl_hash \
  -labsl_cord -labsl_cord_internal -labsl_cordz_functions -labsl_cordz_info -labsl_cordz_sample_token \
  -labsl_status -labsl_statusor \
  -labsl_time -labsl_time_zone \
  -labsl_str_format_internal -labsl_strerror \
  -labsl_debugging_internal -labsl_stacktrace -labsl_symbolize \
  -labsl_log_internal_message -labsl_log_internal_check_op -labsl_log_internal_conditions \
  -labsl_log_internal_format -labsl_log_internal_globals -labsl_log_internal_proto \
  -labsl_log_internal_nullguard -labsl_log_initialize -labsl_log_severity \
  -llog -landroid -ldl -lm -pthread \
  -static-libstdc++ -static-libgcc


echo "[✓] Build concluído com sucesso: rotom_worker_arm64"
