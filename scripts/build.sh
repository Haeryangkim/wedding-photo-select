#!/usr/bin/env bash
# 배포용 빌드. macOS 에서 실행 (lipo 로 유니버설 바이너리 생성).
#   VERSION=v0.2.0 scripts/build.sh   → dist/
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=$VERSION"
rm -rf dist && mkdir -p dist/tmp
export CGO_ENABLED=0

b() { GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "$LDFLAGS" -o "$3" .; echo "built $3"; }

b darwin arm64 dist/tmp/mac-arm64
b darwin amd64 dist/tmp/mac-amd64
lipo -create -output dist/tmp/mac dist/tmp/mac-arm64 dist/tmp/mac-amd64
b windows amd64 dist/tmp/win.exe
b linux amd64 dist/WeddingSelect-linux-amd64
b linux arm64 dist/WeddingSelect-linux-arm64

# zip: "웨딩 사진 셀렉/웨딩 사진 셀렉 시작(.exe)" — 한글 파일명은 UTF-8 플래그로 저장(윈도우 탐색기 호환),
# 맥 실행 권한 유지.
python3 - <<'PY'
import zipfile, time
def mk(out, src, name, mode):
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        d = zipfile.ZipInfo("웨딩 사진 셀렉/", time.localtime()[:6]); d.external_attr = (0o40755 << 16) | 0x10
        z.writestr(d, b"")
        i = zipfile.ZipInfo("웨딩 사진 셀렉/" + name, time.localtime()[:6])
        i.external_attr = mode << 16; i.compress_type = zipfile.ZIP_DEFLATED
        i.create_system = 3
        with open(src, "rb") as f: z.writestr(i, f.read())
mk("dist/WeddingSelect-macos.zip", "dist/tmp/mac", "웨딩 사진 셀렉 시작", 0o100755)
mk("dist/WeddingSelect-windows.zip", "dist/tmp/win.exe", "웨딩 사진 셀렉 시작.exe", 0o100644)
PY
rm -rf dist/tmp
(cd dist && shasum -a 256 * > SHA256SUMS.txt)
ls -la dist
