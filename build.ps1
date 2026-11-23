# quick2api 构建脚本（Windows / PowerShell）
#
# 用法:
#   .\build.ps1                                   # 默认输出到 .\dist\quick2api.dll
#   .\build.ps1 -OutFile C:\out\quick2api.dll     # 指定输出路径
#   .\build.ps1 -ToolRoot C:\q2atools             # 指定便携工具链目录（含 go\ 与 mingw64\）
#
# 前置条件:
#   - Go 1.22+
#   - C 编译器（Windows 下用 mingw-w64，需在 PATH 中；或用 -ToolRoot 指定）
#
# 关于路径: mingw 的 ld.exe 处理不了含非 ASCII 字符的路径，会在链接阶段报
# "cannot find dllcrt2.o"。脚本检测到源码或临时目录含非 ASCII 时，会自动把
# 源码复制到 %TEMP% 下的纯 ASCII 目录构建，再把产物拷回。

param(
    [string]$OutFile = "",
    [string]$ToolRoot = "",
    [string]$GoOS = "windows",
    [string]$GoArch = "amd64"
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$Src = $PSScriptRoot
$hasNonAscii = { param($p) $p -match '[^\x00-\x7F]' }

if ($ToolRoot) {
    $goroot = Join-Path $ToolRoot "go"
    if (Test-Path $goroot) { $env:GOROOT = $goroot }
    $mingwBin = Join-Path $ToolRoot "mingw64\bin"
    if (Test-Path $mingwBin) { $env:PATH = "$mingwBin;$env:PATH" }
}
if ($env:GOROOT) { $env:PATH = "$env:GOROOT\bin;$env:PATH" }

$env:CGO_ENABLED = "1"
$env:GOOS = $GoOS
$env:GOARCH = $GoArch
if (-not $env:GOPROXY) { $env:GOPROXY = "https://proxy.golang.org,direct" }

# 构建目录：源码路径含非 ASCII 时切到临时目录
$work = $Src
$tempWork = $null
if (& $hasNonAscii $Src) {
    $tempWork = Join-Path $env:TEMP ("q2a-build-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
    Write-Host "[build] 源码路径含非 ASCII 字符，改用临时目录: $tempWork"
    New-Item -ItemType Directory -Force -Path $tempWork | Out-Null
    & robocopy $Src $tempWork /E /NFL /NDL /NJH /NJS /NP /R:1 /W:1 /XD .git dist | Out-Null
    if ($LASTEXITCODE -ge 8) { throw "复制源码失败: $LASTEXITCODE" }
    $work = $tempWork
}

# Go 的临时/缓存目录同样不能含非 ASCII
$tmpBase = Join-Path $env:TEMP "q2a-tmp"
if (& $hasNonAscii $tmpBase) { $tmpBase = "C:\q2a-tmp" }
New-Item -ItemType Directory -Force -Path "$tmpBase\gotmp", "$tmpBase\gocache" | Out-Null
$env:GOTMPDIR = "$tmpBase\gotmp"
$env:GOCACHE = "$tmpBase\gocache"
$env:TEMP = "$tmpBase\gotmp"
$env:TMP = "$tmpBase\gotmp"

if (-not $OutFile) { $OutFile = Join-Path $Src "dist\quick2api.dll" }
$finalOut = $OutFile
New-Item -ItemType Directory -Force -Path (Split-Path $finalOut) | Out-Null
# mingw 的链接器写不出非 ASCII 路径：先输出到 ASCII 目录，构建完再用 PowerShell 拷回
if (& $hasNonAscii $OutFile) {
    $OutFile = Join-Path $tmpBase "quick2api.dll"
    Write-Host "[build] 输出路径含非 ASCII 字符，先输出到: $OutFile"
}

Write-Host "[build] go: $(& go version)"
Write-Host "[build] 源码: $work"

Push-Location $work
try {
    & go build -buildmode=c-shared -ldflags="-s -w" -o $OutFile .
    $code = $LASTEXITCODE
} finally {
    Pop-Location
    if ($tempWork) { Remove-Item $tempWork -Recurse -Force -ErrorAction SilentlyContinue }
}
if ($code -ne 0) { throw "构建失败，exit=$code" }

if ($finalOut -ne $OutFile) {
    Copy-Item $OutFile $finalOut -Force
    Write-Host "[build] 已拷回: $finalOut"
}

Write-Host ("[build] 完成: " + $finalOut + "  " + [math]::Round((Get-Item $finalOut).Length / 1MB, 2) + " MB")
