param(
    [string]$OutputDirectory = "build"
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = Join-Path $repositoryRoot $OutputDirectory

$compiler = Get-Command gcc -ErrorAction SilentlyContinue
if (-not $compiler) {
    throw "A MinGW-w64 gcc executable on PATH is required to build the Windows adapter."
}

New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
Push-Location $repositoryRoot
try {
    $env:CGO_ENABLED = "1"
    $env:CC = $compiler.Source
    $moduleCache = (go env GOMODCACHE).Trim()
    $runtimeRoot = Join-Path $moduleCache "github.com\k2-fsa\sherpa-onnx-go-windows@v1.13.5\lib\x86_64-pc-windows-gnu"
    $env:PATH = "$runtimeRoot;$env:PATH"
    go mod verify
    if ($LASTEXITCODE -ne 0) {
        throw "Module verification failed."
    }
    go test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "Go tests failed."
    }
    go build -buildvcs=false -trimpath -o (Join-Path $outputRoot "v-local-cli-sensevoice.exe") .
    if ($LASTEXITCODE -ne 0) {
        throw "Go build failed."
    }

    foreach ($name in @("onnxruntime.dll", "sherpa-onnx-c-api.dll", "sherpa-onnx-cxx-api.dll")) {
        $source = Join-Path $runtimeRoot $name
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "Missing sherpa-onnx Windows runtime library: $name"
        }
        Copy-Item -LiteralPath $source -Destination (Join-Path $outputRoot $name) -Force
    }

    Get-ChildItem -LiteralPath $outputRoot -File | Get-FileHash -Algorithm SHA256 |
        Select-Object Path, Hash
}
finally {
    Pop-Location
}
