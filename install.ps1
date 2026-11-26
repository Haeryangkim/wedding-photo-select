# Wedding Photo Select 설치 (Windows)
#   PowerShell 에서:  irm https://raw.githubusercontent.com/Haeryangkim/wedding-photo-select/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$repo = 'Haeryangkim/wedding-photo-select'
$dir = Join-Path $env:USERPROFILE 'WeddingSelect'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'WeddingSelect.exe'
Write-Host '💍 Wedding Photo Select 설치 중… (약 30MB)'
$ProgressPreference = 'SilentlyContinue'
Get-Process WeddingSelect -ErrorAction SilentlyContinue | Stop-Process -Force
Invoke-WebRequest "https://github.com/$repo/releases/latest/download/WeddingSelect-windows.exe" -OutFile $exe -UseBasicParsing

# 바탕화면 바로가기
$desktop = [Environment]::GetFolderPath('Desktop')
$ws = New-Object -ComObject WScript.Shell
$lnk = $ws.CreateShortcut((Join-Path $desktop '웨딩 사진 셀렉.lnk'))
$lnk.TargetPath = $exe
$lnk.WorkingDirectory = $dir
$lnk.Save()

Write-Host ''
Write-Host '✅ 설치 완료! 다음부터는 바탕화면의 [웨딩 사진 셀렉] 을 더블클릭하세요.'
Write-Host "   사진 폴더: $dir\사진"
Start-Process -FilePath $exe -WorkingDirectory $dir
