#!/bin/sh
# Wedding Photo Select 설치 (macOS / Linux)
#   curl -fsSL https://raw.githubusercontent.com/Haeryangkim/wedding-photo-select/main/install.sh | sh
set -e
REPO="Haeryangkim/wedding-photo-select"
DIR="$HOME/WeddingSelect"
OS=$(uname -s); ARCH=$(uname -m)
case "$OS" in
  Darwin) ASSET="WeddingSelect-macos" ;;
  Linux)
    case "$ARCH" in
      x86_64|amd64) ASSET="WeddingSelect-linux-amd64" ;;
      aarch64|arm64) ASSET="WeddingSelect-linux-arm64" ;;
      *) echo "지원하지 않는 CPU: $ARCH"; exit 1 ;;
    esac ;;
  *) echo "지원하지 않는 OS: $OS (Windows 는 README 의 설치 방법을 보세요)"; exit 1 ;;
esac

echo "💍 Wedding Photo Select 설치 중…"
mkdir -p "$DIR"
curl -fL --progress-bar "https://github.com/$REPO/releases/latest/download/$ASSET" -o "$DIR/WeddingSelect.new"
chmod +x "$DIR/WeddingSelect.new"
mv -f "$DIR/WeddingSelect.new" "$DIR/WeddingSelect"

if [ "$OS" = "Darwin" ]; then
  # 바탕화면에 더블클릭 실행 아이콘
  L="$HOME/Desktop/웨딩 사진 셀렉.command"
  printf '#!/bin/sh\ncd "%s" && exec ./WeddingSelect\n' "$DIR" > "$L"
  chmod +x "$L"
  echo ""
  echo "✅ 설치 완료!  다음부터는 바탕화면의 [웨딩 사진 셀렉] 을 더블클릭하세요."
else
  echo ""
  echo "✅ 설치 완료!  다음부터는 $DIR/WeddingSelect 를 실행하세요."
fi
echo "   사진 폴더: $DIR/사진"
echo ""
cd "$DIR" && exec ./WeddingSelect
