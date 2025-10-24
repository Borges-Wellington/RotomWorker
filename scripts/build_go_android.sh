#!/usr/bin/env bash
set -e
NDK_ROOT=${ANDROID_NDK_HOME:-$HOME/android-ndk}
if [ -z "$NDK_ROOT" ]; then
  echo "Set ANDROID_NDK_HOME or install NDK to \$HOME/android-ndk"
  exit 1
fi

# Cross compile Go for android/arm64
export GOOS=android
export GOARCH=arm64
export CGO_ENABLED=1
export CC=$NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang
export CXX=$NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang++

echo "Building rotom_worker (android/arm64)..."
go build -o rotom_worker main.go
echo "Done: rotom_worker"
