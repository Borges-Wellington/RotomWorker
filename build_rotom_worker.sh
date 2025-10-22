#!/bin/bash
set -euo pipefail

echo "[+] Compilando RotomWorker (modo host)"

CXX=${CXX:-g++}
CXXFLAGS="-std=c++17 -O2 -Wall -Wextra -DASIO_STANDALONE -D_WEBSOCKETPP_CPP11_STL_ -D_WEBSOCKETPP_CPP11_THREAD_"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
INCLUDE_DIR="$SRC_DIR/include"
PROTO_GEN_DIR="$SRC_DIR/proto_gen"
OUTPUT="$SRC_DIR/rotom_worker"

INCLUDES=(
  -I"$INCLUDE_DIR"
  -I"$INCLUDE_DIR/asio"
  -I"$INCLUDE_DIR/websocketpp"
  -I"$INCLUDE_DIR/nlohmann"
  -I"$PROTO_GEN_DIR"
)

SOURCES=(
  "$SRC_DIR/main.cpp"
  "$PROTO_GEN_DIR/rotom.pb.cc"
)

LIBS=(
  -lpthread
  -ldl
)

echo "[+] Utilizando compilador: $CXX"
"$CXX" $CXXFLAGS "${INCLUDES[@]}" "${SOURCES[@]}" "${LIBS[@]}" -o "$OUTPUT"

echo "[✓] Build concluído com sucesso: $OUTPUT"
