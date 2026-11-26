#!/usr/bin/env bash
# 배포용 빌드. macOS 에서 실행 (lipo 로 유니버설 바이너리 생성).
#   VERSION=v0.1.0 scripts/build.sh   → dist/
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=$VERSION"
rm -rf dist && mkdir -p dist/tmp
export CGO_ENABLED=0

b() { GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "$LDFLAGS" -o "$3" .; echo "built $3"; }

b darwin arm64 dist/tmp/mac-arm64
b darwin amd64 dist/tmp/mac-amd64
lipo -create -output dist/WeddingSelect-macos dist/tmp/mac-arm64 dist/tmp/mac-amd64
b windows amd64 dist/WeddingSelect-windows.exe
b linux amd64 dist/WeddingSelect-linux-amd64
b linux arm64 dist/WeddingSelect-linux-arm64

# 맥 수동 다운로드용 zip (실행 권한 유지)
APPDIR="dist/tmp/웨딩 사진 셀렉"
mkdir -p "$APPDIR"
cp dist/WeddingSelect-macos "$APPDIR/웨딩 사진 셀렉 시작"
chmod +x "$APPDIR/웨딩 사진 셀렉 시작"
(cd dist/tmp && zip -qry ../WeddingSelect-macos.zip "웨딩 사진 셀렉")
rm -rf dist/tmp
(cd dist && shasum -a 256 * > SHA256SUMS.txt)
ls -la dist
