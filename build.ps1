# quick2api 插件构建脚本
# 由于 mingw 的 ld.exe 无法处理中文路径，构建在纯 ASCII 目录 C:\q2abuild 进行，
# 产物再拷回工作区。工具链位于 C:\q2atools（纯 ASCII 物理路径）。
$ErrorActionPreference = "Stop"
$WS = "C:\zAIproduction\深度空间2\cliproxyapi-quick2api"
$BUILD = "C:\q2abuild"
$TOOLS = "C:\q2atools"

$env:GOROOT = "$TOOLS\go"
$env:PATH = "$env:GOROOT\bin;$TOOLS\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:GOTMPDIR = "$BUILD\gotmp"
$env:GOCACHE = "$BUILD\gocache"
$env:TEMP = "$BUILD\gotmp"
$env:TMP = "$BUILD\gotmp"

$env:GOPROXY = "https://goproxy.cn,https://proxy.golang.org,direct"
New-Item -ItemType Directory -Force -Path "$BUILD\gotmp","$BUILD\gocache","$BUILD\dist" | Out-Null

Write-Output "== 同步源码 =="
& robocopy "$WS\src" "$BUILD\src" /MIR /NFL /NDL /NJH /NJS /NP /R:1 /W:1 2>&1 | Out-Null
if ($LASTEXITCODE -ge 8) { throw "robocopy failed: $LASTEXITCODE" }

Write-Output "== gofmt =="
Push-Location "$BUILD\src"
& gofmt -w .
Pop-Location

Write-Output "== go build -buildmode=c-shared =="
Push-Location "$BUILD\src"
$out = "$BUILD\dist\quick2api.dll"
& go build -buildmode=c-shared -ldflags="-s -w" -o $out . 2>&1
$code = $LASTEXITCODE
Pop-Location
if ($code -ne 0) { throw "go build failed: $code" }

$final = "$WS\dist\quick2api.dll"
New-Item -ItemType Directory -Force -Path (Split-Path $final) | Out-Null
Copy-Item $out $final -Force
Write-Output ("BUILD OK: " + $final + "  size=" + (Get-Item $final).Length)
