# 构建脚本：产出两个 exe
#
#   mgdic.exe       GUI 子系统（-H windowsgui）——双击启动没有控制台窗口，只出托盘图标
#   mgdic-cli.exe   控制台子系统——给 q / import / config / --selfcheck 这些命令用
#
# 同一个代码，只有链接参数不同。GUI 版也保留了 AttachConsole：从 cmd 里跑
# `mgdic.exe q apple` 时会挂到父控制台上，输出照样看得见。

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$go = Join-Path $env:LOCALAPPDATA 'Programs\Go\go\bin\go.exe'
if (-not (Test-Path $go)) { $go = 'go' }

New-Item -ItemType Directory -Force -Path (Join-Path $root 'bin') | Out-Null

Write-Host '编译 GUI 版（无控制台）…'
& $go build -trimpath -ldflags '-H windowsgui -s -w' -o (Join-Path $root 'bin\mgdic.exe') .\cmd\mgdic
if ($LASTEXITCODE -ne 0) { throw "GUI 版编译失败" }

Write-Host '编译 CLI 版（有控制台）…'
& $go build -trimpath -ldflags '-s -w' -o (Join-Path $root 'bin\mgdic-cli.exe') .\cmd\mgdic
if ($LASTEXITCODE -ne 0) { throw "CLI 版编译失败" }

Get-ChildItem (Join-Path $root 'bin') -Filter *.exe |
    ForEach-Object { '{0,12:N0}  {1}' -f $_.Length, $_.Name }
Write-Host '完成。'
