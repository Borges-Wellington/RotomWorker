#!/bin/bash
set -e

echo -e "\033[1;33m==> Build do RotomWorker (NDK + Protobuf + Abseil interno)\033[0m"

NDK=$HOME/android-ndk
TOOLCHAIN=$NDK/toolchains/llvm/prebuilt/linux-x86_64
SYSROOT=$TOOLCHAIN/sysroot

BASE_DIR=$(pwd)
PROTOBUF_DIR=$HOME/Documentos/Wellington/Lab/protobuf
ROTOMPROTOS_DIR=$HOME/Documentos/Wellington/Lab/RotomProtos
ABSL_BUILD=$PROTOBUF_DIR/build_android/_deps/absl-build

echo -e "\033[1;32mGerando arquivos .pb...\033[0m"
/usr/local/bin/protoc \
  --proto_path=$ROTOMPROTOS_DIR \
  --cpp_out=proto_gen \
  $ROTOMPROTOS_DIR/rotom.proto

echo -e "\033[1;32mCompilando rotom_worker...\033[0m"

$TOOLCHAIN/bin/aarch64-linux-android24-clang++ \
  -std=c++17 -fPIE -pie -DASIO_STANDALONE \
  --sysroot=$SYSROOT \
  -Iproto_gen \
  -I$PROTOBUF_DIR/src \
  -I$PROTOBUF_DIR/build_android/_deps/absl-src \
  -I$PROTOBUF_DIR/build_android/_deps/absl-src/absl \
  -I$PROTOBUF_DIR/third_party/utf8_range \
  -I$BASE_DIR/third_party/asio/asio/include \
  -I$BASE_DIR/third_party/nlohmann \
  -I$BASE_DIR/third_party/websocketpp \
  main.cpp proto_gen/rotom.pb.cc \
  $PROTOBUF_DIR/third_party/utf8_range/utf8_range.c \
  \
  # ======= PROTOBUF =======
 # $PROTOBUF_DIR/build_android/libprotobuf.a \
 # $PROTOBUF_DIR/build_android/libprotobuf-lite.a \
 # \
  # ======= ABSEIL (ordem real) =======
  $ABSL_BUILD/absl/base/libabsl_base.a \
  $ABSL_BUILD/absl/base/libabsl_spinlock_wait.a \
  $ABSL_BUILD/absl/base/libabsl_raw_logging_internal.a \
  $ABSL_BUILD/absl/base/libabsl_log_severity.a \
  $ABSL_BUILD/absl/base/libabsl_malloc_internal.a \
  $ABSL_BUILD/absl/base/libabsl_tracing_internal.a \
  $ABSL_BUILD/absl/strings/libabsl_strings_internal.a \
  $ABSL_BUILD/absl/strings/libabsl_strings.a \
  $ABSL_BUILD/absl/strings/libabsl_str_format_internal.a \
  $ABSL_BUILD/absl/strings/libabsl_cord_internal.a \
  $ABSL_BUILD/absl/strings/libabsl_cord.a \
  $ABSL_BUILD/absl/synchronization/libabsl_graphcycles_internal.a \
  $ABSL_BUILD/absl/synchronization/libabsl_kernel_timeout_internal.a \
  $ABSL_BUILD/absl/synchronization/libabsl_synchronization.a \
  $ABSL_BUILD/absl/time/libabsl_time.a \
  $ABSL_BUILD/absl/time/libabsl_civil_time.a \
  $ABSL_BUILD/absl/time/libabsl_time_zone.a \
  $ABSL_BUILD/absl/status/libabsl_status.a \
  $ABSL_BUILD/absl/status/libabsl_statusor.a \
  $ABSL_BUILD/absl/hash/libabsl_city.a \
  $ABSL_BUILD/absl/hash/libabsl_hash.a \
  $ABSL_BUILD/absl/container/libabsl_container_common.a \
  $ABSL_BUILD/absl/container/libabsl_raw_hash_set.a \
  $ABSL_BUILD/absl/container/libabsl_hashtablez_sampler.a \
  $ABSL_BUILD/absl/numeric/libabsl_int128.a \
  $ABSL_BUILD/absl/log/libabsl_log_internal_check_op.a \
  $ABSL_BUILD/absl/log/libabsl_log_internal_conditions.a \
  $ABSL_BUILD/absl/log/libabsl_log_internal_globals.a \
  $ABSL_BUILD/absl/log/libabsl_log_internal_message.a \
  $ABSL_BUILD/absl/log/libabsl_log_internal_proto.a \
  $ABSL_BUILD/absl/log/libabsl_log_globals.a \
  $ABSL_BUILD/absl/log/libabsl_log_initialize.a \
  $ABSL_BUILD/absl/log/libabsl_log_sink.a \
  $ABSL_BUILD/absl/log/libabsl_vlog_config_internal.a \
  \
  -ldl -pthread -static-libstdc++ -s \
  -o rotom_worker

echo -e "\033[1;32m✅ Build concluído com sucesso! Binário: rotom_worker\033[0m"
