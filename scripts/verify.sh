#!/usr/bin/env bash
# 一键验证：前端构建（main.go 嵌入 dist，必须先于 go vet）/ vet / 测试 / race / 生产打包
set -uo pipefail
cd "$(dirname "$0")/.."
export PATH="$PATH:$HOME/go/bin"

fail=0

# main.go 用 //go:embed all:frontend/dist，干净检出时该目录不存在会让 vet 失败。
echo "==> frontend build (dist needed by go:embed)"
(cd frontend && npm run --silent build) || fail=1

echo "==> go vet"
vet_out="$(go vet ./internal/... . 2>&1 | grep -vE 'ld: warning|object file' || true)"
if [ -n "$vet_out" ]; then echo "$vet_out"; fail=1; fi

echo "==> go test"
go test ./internal/... || fail=1

echo "==> go test -race"
go test -race ./internal/... || fail=1

echo "==> wails3 build"
if ! wails3 build; then
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "VERIFY: FAILED"
  exit 1
fi
echo "VERIFY: ALL GREEN"
