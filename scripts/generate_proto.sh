#!/bin/bash
set -e
echo "==> Gerando arquivos Protobuf (Go e C++)"

PROTO_SRC=proto_src/rotom.proto
PROTO_GEN=proto_gen

mkdir -p $PROTO_GEN

protoc --go_out=$PROTO_GEN --go_opt=paths=source_relative $PROTO_SRC
protoc --cpp_out=$PROTO_GEN $PROTO_SRC

echo "✓ Protobufs gerados em $PROTO_GEN"
