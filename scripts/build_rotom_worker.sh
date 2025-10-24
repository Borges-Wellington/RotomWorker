#!/bin/bash
set -e
echo -e "\033[1;33m==> Build do RotomWorker híbrido (Go + C++)\033[0m"

NDK=$HOME/android-ndk
TOOLCHAIN=$NDK/toolchains/llvm/prebuilt/linux-x86_64
SYSROOT=$TOOLCHAIN/sysroot
BUILD_DIR=cpp/build

mkdir -p $BUILD_DIR
cd $BUILD_DIR

cmake .. -DCMAKE_TOOLCHAIN_FILE=$NDK/build/cmake/android.toolchain.cmake \
  -DANDROID_ABI=arm64-v8a \
  -DANDROID_PLATFORM=android-28
make -j$(nproc)

cd ../../
go build -o rotom_worker main.go
echo "✓ Build concluído!"
