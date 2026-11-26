#!/bin/sh
# Wedding Photo Select 설치 (macOS / Linux)
#   curl -fsSL https://raw.githubusercontent.com/Haeryangkim/wedding-photo-select/main/install.sh | sh
#
# 바탕화면에 "웨딩 사진 셀렉" 폴더 하나를 만들고 모든 파일(프로그램·사진·점수·설정)을 그 안에 둡니다.
# 삭제는 그 폴더를 휴지통에 버리면 끝.
set -e
REPO="Haeryangkim/wedding-photo-select"
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

if [ -d "$HOME/Desktop" ]; then BASE="$HOME/Desktop"; else BASE="$HOME"; fi
DIR="$BASE/웨딩 사진 셀렉"
APP="$DIR/웨딩 사진 셀렉 시작"

echo "💍 Wedding Photo Select 설치 중…"
mkdir -p "$DIR"
curl -fL --progress-bar "https://github.com/$REPO/releases/latest/download/$ASSET" -o "$APP.new"
chmod +x "$APP.new"
mv -f "$APP.new" "$APP"

# v0.1.0 설치 흔적 정리 (홈 폴더 설치 방식이었음)
rm -f "$HOME/Desktop/웨딩 사진 셀렉.command" 2>/dev/null || true

echo ""
echo "✅ 설치 완료!"
echo "   폴더: $DIR"
echo "   다음부터는 이 폴더 안의 [웨딩 사진 셀렉 시작] 을 더블클릭하세요."
echo "   삭제하려면 이 폴더를 통째로 휴지통에 버리면 됩니다."
if [ "$OS" = "Darwin" ] && [ -d "$HOME/Library/Mobile Documents/com~apple~CloudDocs/Desktop" ]; then
  echo ""
  echo "⚠️  iCloud 에 '데스크탑' 동기화가 켜져 있어요. 사진이 iCloud 에도 올라갈 수 있어요."
  echo "   원치 않으면 폴더를 다른 곳(예: 홈 폴더)으로 옮겨서 쓰세요. 옮겨도 그대로 동작해요."
fi
if [ -d "$HOME/WeddingSelect" ]; then
  echo ""
  echo "ℹ️  예전 설치 폴더 $HOME/WeddingSelect 가 있어요. 필요 없으면 지워도 됩니다."
fi
echo ""
cd "$DIR" && exec "./웨딩 사진 셀렉 시작"
