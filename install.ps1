# Wedding Photo Select 설치 (Windows)
#   PowerShell 에서:  irm https://raw.githubusercontent.com/Haeryangkim/wedding-photo-select/main/install.ps1 | iex
#
# 바탕화면에 "웨딩 사진 셀렉" 폴더 하나를 만들고 모든 파일(프로그램·사진·점수·설정)을 그 안에 둡니다.
# 삭제는 그 폴더를 휴지통에 버리면 끝.
$ErrorActionPreference = 'Stop'
$repo = 'Haeryangkim/wedding-photo-select'
$desktop = [Environment]::GetFolderPath('Desktop')
$dir = Join-Path $desktop '웨딩 사진 셀렉'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir '웨딩 사진 셀렉 시작.exe'
Write-Host '💍 Wedding Photo Select 설치 중… (약 25MB)'
$ProgressPreference = 'SilentlyContinue'
Get-Process '웨딩 사진 셀렉 시작', 'WeddingSelect' -ErrorAction SilentlyContinue | Stop-Process -Force
Invoke-WebRequest "https://github.com/$repo/releases/latest/download/WeddingSelect-windows.exe" -OutFile $exe -UseBasicParsing

# v0.1.0 설치 흔적 정리 (홈 폴더 설치 방식이었음)
Remove-Item (Join-Path $desktop '웨딩 사진 셀렉.lnk') -ErrorAction SilentlyContinue

Write-Host ''
Write-Host '✅ 설치 완료!'
Write-Host "   폴더: $dir"
Write-Host '   다음부터는 이 폴더 안의 [웨딩 사진 셀렉 시작] 을 더블클릭하세요.'
Write-Host '   삭제하려면 이 폴더를 통째로 휴지통에 버리면 됩니다.'
if ($desktop -like '*OneDrive*') {
  Write-Host ''
  Write-Host '⚠️  바탕화면이 OneDrive 와 동기화되고 있어요. 사진이 OneDrive 에도 올라갈 수 있어요.'
  Write-Host '   원치 않으면 폴더를 다른 곳(예: C:\웨딩 사진 셀렉)으로 옮겨서 쓰세요. 옮겨도 그대로 동작해요.'
}
if (Test-Path (Join-Path $env:USERPROFILE 'WeddingSelect')) {
  Write-Host ''
  Write-Host "ℹ️  예전 설치 폴더 $env:USERPROFILE\WeddingSelect 가 있어요. 필요 없으면 지워도 됩니다."
}
Start-Process -FilePath $exe -WorkingDirectory $dir
