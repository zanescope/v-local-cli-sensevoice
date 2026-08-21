param(
    [switch]$Release,
    [string]$CertificateThumbprint,
    [string]$SignToolPath,
    [ValidatePattern('^https://')]
    [string]$TimestampUrl = 'https://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$Version = (Get-Content -Raw (Join-Path $ProjectRoot 'VERSION')).Trim()
if ($Version -notmatch '^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
    throw "VERSION 不是可发布的 SemVer：$Version"
}

Push-Location $ProjectRoot
try {
    & (Join-Path $PSScriptRoot 'build.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'SenseVoice 构建失败' }
}
finally {
    Pop-Location
}

$BuildRoot = Join-Path $ProjectRoot 'build'
$Binary = Join-Path $BuildRoot 'v-local-cli-sensevoice.exe'
$RuntimeFiles = @(
    $Binary,
    (Join-Path $BuildRoot 'onnxruntime.dll'),
    (Join-Path $BuildRoot 'sherpa-onnx-c-api.dll'),
    (Join-Path $BuildRoot 'sherpa-onnx-cxx-api.dll')
)
$ReportedVersion = (& $Binary --version).Trim()
if ($LASTEXITCODE -ne 0 -or $ReportedVersion -ne $Version) {
    throw "二进制版本 $ReportedVersion 与 VERSION $Version 不一致"
}

$Signed = $false
if ($Release) {
    if (-not $CertificateThumbprint) { throw '-Release 必须同时提供 -CertificateThumbprint' }
    if ($SignToolPath) {
        $ResolvedSignTool = (Resolve-Path -LiteralPath $SignToolPath -ErrorAction Stop).Path
    }
    else {
        $SignTool = Get-Command signtool.exe -ErrorAction SilentlyContinue
        if (-not $SignTool) { throw '未找到 SignTool；请安装 Windows SDK 或传入 -SignToolPath' }
        $ResolvedSignTool = $SignTool.Source
    }
    foreach ($File in $RuntimeFiles) {
        & $ResolvedSignTool sign /fd SHA256 /sha1 $CertificateThumbprint /tr $TimestampUrl /td SHA256 $File
        if ($LASTEXITCODE -ne 0) { throw "Authenticode 签名失败：$File" }
        & $ResolvedSignTool verify /pa /all $File
        if ($LASTEXITCODE -ne 0) { throw "Authenticode 验证失败：$File" }
        $Signature = Get-AuthenticodeSignature -LiteralPath $File
        if ($Signature.Status -ne 'Valid' -or -not $Signature.TimeStamperCertificate) {
            throw "发布件缺少有效签名或可信时间戳：$File"
        }
    }
    $Signed = $true
}

$ModuleCache = (go env GOMODCACHE).Trim()
$UpstreamLicense = Join-Path $ModuleCache 'github.com\k2-fsa\sherpa-onnx-go-windows@v1.13.5\LICENSE'
if (-not (Test-Path -LiteralPath $UpstreamLicense -PathType Leaf)) {
    throw '缺少 sherpa-onnx-go-windows Apache-2.0 许可证'
}

$DistRoot = Join-Path $ProjectRoot 'dist'
$PackageName = "v-local-cli-sensevoice-$Version-windows-amd64"
$StageRoot = Join-Path $DistRoot $PackageName
$Archive = Join-Path $DistRoot "$PackageName.zip"
if ((Test-Path -LiteralPath $StageRoot) -or (Test-Path -LiteralPath $Archive)) {
    throw "发布输出已存在，请使用干净工作区：$PackageName"
}
New-Item -ItemType Directory -Force $StageRoot | Out-Null
foreach ($File in $RuntimeFiles) { Copy-Item -LiteralPath $File -Destination $StageRoot }
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'LICENSE') -Destination $StageRoot
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'THIRD_PARTY_NOTICES.md') -Destination $StageRoot
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'MODEL_SOURCES.md') -Destination $StageRoot
Copy-Item -LiteralPath $UpstreamLicense -Destination (Join-Path $StageRoot 'THIRD_PARTY-LICENSE-Apache-2.0.txt')

$Checksums = foreach ($File in Get-ChildItem -LiteralPath $StageRoot -File | Where-Object { $_.Extension -in @('.exe', '.dll') } | Sort-Object Name) {
    $Hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $File.FullName).Hash.ToLowerInvariant()
    "$Hash *$($File.Name)"
}
[IO.File]::WriteAllLines((Join-Path $StageRoot 'checksums.txt'), $Checksums, [Text.UTF8Encoding]::new($false))
$Manifest = [ordered]@{
    schema_version = 1
    name = 'v-local-cli-sensevoice'
    version = $Version
    protocol = 'v-local-cli-asr/1'
    platform = 'windows'
    arch = 'amd64'
    signed = $Signed
    signature = if ($Signed) { 'authenticode' } else { 'none' }
    timestamped = $Signed
    sherpa_onnx_go_version = 'v1.13.5'
    model_included = $false
} | ConvertTo-Json
[IO.File]::WriteAllText((Join-Path $StageRoot 'manifest.json'), $Manifest, [Text.UTF8Encoding]::new($false))

Compress-Archive -Path (Join-Path $StageRoot '*') -DestinationPath $Archive -CompressionLevel Optimal
$ArchiveDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $Archive).Hash.ToLowerInvariant()
[IO.File]::WriteAllText(
    (Join-Path $DistRoot 'checksums.txt'),
    "$ArchiveDigest *$([IO.Path]::GetFileName($Archive))`n",
    [Text.UTF8Encoding]::new($false)
)

[ordered]@{
    version = $Version
    archive = $Archive
    sha256 = $ArchiveDigest
    signed = $Signed
} | ConvertTo-Json
