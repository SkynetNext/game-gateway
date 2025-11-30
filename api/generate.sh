#!/bin/bash

# Protobuf 代码生成脚本
# 用于从 gateway.proto 生成 Go 代码

set -e

TOOLS_DIR="../tools"
PROTO_FILE="$SCRIPT_DIR/gateway.proto"
PROTOC="$TOOLS_DIR/protoc/bin/protoc.exe"

"$PROTOC" \
    --go_out=. \
    --go_opt=paths=source_relative \
    --go-grpc_out=. \
    --go-grpc_opt=paths=source_relative \
    gateway.proto

