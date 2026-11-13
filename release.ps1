<#
.SYNOPSIS
    一条命令发版：改版本号 → 编译 → 测试 → 打 tag → 推送 → 建 GitHub Release → 传资产。

.DESCRIPTION
    版本号只改 internal/version/version.go 那一处（本脚本会替你改）。

    凭据优先级：$env:GH_TOKEN > Git 凭据管理器里的缓存。
    脚本**从不打印 token**，只报"有没有、能不能用"。

.PARAMETER Version
    新版本号，例如 0.2.0。不填则用 version.go 里现有的值（只重发，不改号）。

.PARAMETER DryRun
    只做到本地编译 + 打 tag，不推送、不建 Release。用来验证脚本本身。

.PARAMETER Draft
    发布成草稿，不公开。

.EXAMPLE
    .\release.ps1 -Version 0.2.0
    .\release.ps1 -Version 0.2.1 -Draft
    .\release.ps1 -DryRun
#>
param(
    [string]$Version,
    [switch]$DryRun,
    [switch]$Draft
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$repo = 'markgoo/mgdic'
$verFile = Join-Path $root 'internal\version\version.go'
$go = Join-Path $env:LOCALAPPDATA 'Programs\Go\go\bin\go.exe'
if (-not (Test-Path $go)) { $go = 'go' }

function Step($msg) { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Ok($msg) { Write-Host "    $msg" -ForegroundColor Green }
function Warn($msg) { Write-Host "    $msg" -ForegroundColor Yellow }

# ---------- 1. 版本号 ----------
Step '版本号'
$current = (Select-String -Path $verFile -Pattern 'const Version = "([^"]+)"').Matches[0].Groups[1].Value
if (-not $Version) {
    $Version = $current
    Ok "沿用现有版本 $Version"
} elseif ($Version -ne $current) {
    $c = Get-Content $verFile -Raw
    $c = $c -replace 'const Version = "[^"]+"', "const Version = `"$Version`""
    Set-Content $verFile -Value $c -Encoding UTF8 -NoNewline
    Ok "$current -> $Version（已写入 version.go）"
} else {
    Ok "版本已是 $Version"
}
$tag = "v$Version"

if ((git tag -l $tag)) { throw "tag $tag 已存在。要重发请先删：git tag -d $tag; git push origin :refs/tags/$tag" }

# ---------- 2. 工作区检查 ----------
Step '工作区'
$dirty = git status --porcelain
$expected = @('M  internal/version/version.go', 'M internal/version/version.go')
$unexpected = $dirty | Where-Object { $_.Trim() -ne '' -and ($_ -notmatch 'version\.go') }
if ($unexpected) {
    Warn '还有别的未提交改动：'
    $unexpected | ForEach-Object { Write-Host "      $_" }
    throw '先提交或还原这些改动再发版（避免把半成品打进 Release）'
}
Ok '干净（或只有版本号改动）'

# ---------- 3. 编译 ----------
Step '编译'
& $go build ./... 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'go build 失败' }
& (Join-Path $root 'build.ps1') 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'build.ps1 失败' }
$assets = @(
    (Join-Path $root 'bin\mgdic.exe'),
    (Join-Path $root 'bin\mgdic-cli.exe')
)
foreach ($a in $assets) {
    if (-not (Test-Path $a)) { throw "缺少产物: $a" }
    Ok ("{0}  {1:N1} MB" -f (Split-Path $a -Leaf), ((Get-Item $a).Length / 1MB))
}

# ---------- 4. 测试 ----------
Step '测试'
& $go vet ./... 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'go vet 失败' }
& $go test ./... 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'go test 失败' }
Ok 'vet / test 通过'

# ---------- 5. 提交 + tag ----------
Step '提交与打 tag'
git add -A
$staged = git diff --cached --name-only
if ($staged) {
    git -c core.safecrlf=false commit -q -m "发布 $tag"
    Ok "已提交版本号变更"
} else {
    Ok '无需提交'
}
git tag -a $tag -m "mgdic $tag"
Ok "已打 tag $tag"

if ($DryRun) {
    Warn 'DryRun：到此为止，没有推送、没有建 Release'
    Write-Host "`n要撤销本地 tag: git tag -d $tag" -ForegroundColor Yellow
    return
}

# ---------- 6. 推送 ----------
Step '推送到 GitHub'
$env:GIT_TERMINAL_PROMPT = '0'
git push 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'git push 失败（凭据过期？删掉缓存的凭据后重试会弹浏览器授权）' }
git push origin $tag 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw "推送 tag 失败" }
Ok 'main 与 tag 都已推送'

# ---------- 7. 凭据 ----------
Step 'GitHub 凭据'
$token = $env:GH_TOKEN
$src = 'GH_TOKEN 环境变量'
if (-not $token) {
    $cred = "protocol=https`nhost=github.com`n`n" | git credential fill 2>$null
    $token = ($cred | Where-Object { $_ -like 'password=*' }) -replace '^password=',''
    $src = 'Git 凭据管理器'
}
if (-not $token) { throw '没有可用凭据。设 $env:GH_TOKEN，或先 git push 一次让凭据管理器存下来' }
Ok "已取得凭据（来源：$src，长度 $($token.Length)，内容不显示）"

$h = @{
    Authorization          = "token $token"
    Accept                 = 'application/vnd.github+json'
    'User-Agent'           = 'mgdic-release'
}

# ---------- 8. 建 Release ----------
Step "创建 Release $tag"
$notes = @"
轻量本地词典：Alt+E 呼出，输入词 / 词组 / 句子，看结果，复制走。

## 怎么用

1. 下载下面的 ``mgdic.exe``
2. 双击（没有安装过程，绿色版）
3. **首次运行会自动下载词典数据（66MB）并建库（约 10 秒）**
4. 之后按 Alt+E 呼出，托盘常驻，启动 0.2 秒

整句翻译需要在 ``config.json`` 里填 DeepSeek 的 ``api_key``；不填也能用，只是句子只给逐词结果。

## 文件

| 文件 | 说明 |
|---|---|
| ``mgdic.exe`` | GUI 版，双击没有控制台窗口，只出托盘图标 |
| ``mgdic-cli.exe`` | 命令行版：``q`` 查词 / ``import`` 建库 / ``config`` 看配置 / ``--selfcheck`` 诊断 |

## 数据

词典数据不随本发布分发，由程序在首次运行时从
[ECDICT](https://github.com/skywind3000/ECDICT)（MIT 许可）下载，版权归其作者。
"@
$payload = @{
    tag_name   = $tag
    name       = "mgdic $tag"
    body       = $notes
    draft      = [bool]$Draft
    prerelease = $false
} | ConvertTo-Json -Depth 5

try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases" -Headers $h -Method Post `
        -Body ([Text.Encoding]::UTF8.GetBytes($payload)) -ContentType 'application/json; charset=utf-8'
    Ok "Release: $($rel.html_url)"
} catch {
    throw "创建 Release 失败: $($_.Exception.Message) $($_.ErrorDetails.Message)"
}

# ---------- 9. 传资产 ----------
Step '上传资产'
foreach ($a in $assets) {
    $name = Split-Path $a -Leaf
    $bytes = [IO.File]::ReadAllBytes($a)
    Write-Host "    上传 $name（{0:N1} MB）…" -f ($bytes.Length / 1MB)
    try {
        $r = Invoke-RestMethod -Uri "https://uploads.github.com/repos/$repo/releases/$($rel.id)/assets?name=$name" `
            -Headers $h -Method Post -Body $bytes -ContentType 'application/octet-stream'
        Ok "  $($r.browser_download_url)"
    } catch {
        throw "上传 $name 失败: $($_.Exception.Message) $($_.ErrorDetails.Message)"
    }
}

Step '完成'
Write-Host "  Release: $($rel.html_url)" -ForegroundColor Green
Write-Host "  版本:    $tag" -ForegroundColor Green
