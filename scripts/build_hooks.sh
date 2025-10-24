#!/usr/bin/env bash
set -euo pipefail

NDK=$HOME/android-ndk
TOOLCHAIN=$NDK/toolchains/llvm/prebuilt/linux-x86_64
API=21
AARCH64_CXX=$TOOLCHAIN/bin/aarch64-linux-android${API}-clang++

$AARCH64_CXX -fPIC -O2 libshim_niantic.cpp -o libshim_niantic.so -shared -ldl
