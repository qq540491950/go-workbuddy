#!/usr/bin/env bash
# 一键验证：后端测试 / -race / vet / 前端类型检查与构建 / 生产打包
set -uo pipefail
cd "$(dirname "$0")/.."
export PATH="$PATH:$HOME/go/bin"

fail=0

echo "==> go vet"
vet_out="$(go vet ./internal/... . 2>&1 | grep -vE 'ld: warning|object file' || true)"
if [ -n "$vet_out" ]; then echo "$vet_out"; fail=1; fi

echo "==> go test"
go test ./internal/... || fail=1

echo "==> go test -race (services/agentkit/mcpmgr)"
go test -race ./internal/services/ ./internal/agentkit/ ./internal/mcpmgr/ || fail=1

echo "==> frontend tsc + build"
(cd frontend && npm run --silent build) || fail=1

echo "==> wails3 build"
wails3 build >/dev/null 2>&1 || fail=1

if [ "$fail" -ne 0 ]; then
  echo "VERIFY: FAILED"
  exit 1
fi
echo "VERIFY: ALL GREEN"
