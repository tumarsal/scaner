# Сборка scaner (аналог Makefile для Windows)
# Использование: .\build.ps1 [build|build-all|clean|clean-all]

param(
    [ValidateSet("build", "build-all", "clean", "clean-all")]
    [string]$Target = "build"
)

$ErrorActionPreference = "Stop"

# Подгрузка .env если есть
if (Test-Path .env) {
    Get-Content .env | ForEach-Object {
        if ($_ -match "^\s*([^#][^=]+)=(.*)$") {
            [System.Environment]::SetEnvironmentVariable($matches[1].Trim(), $matches[2].Trim(), "Process")
        }
    }
}

$BINARY_NAME = if ($env:BINARY_NAME) { $env:BINARY_NAME } else { "scaner" }
$BUILD_DIR   = if ($env:BUILD_DIR)   { $env:BUILD_DIR }   else { "build" }
$GOOS_LIST   = @("linux", "darwin", "windows")
$GOARCH_LIST = @("amd64", "arm64")

function Build-One {
    New-Item -ItemType Directory -Force -Path $BUILD_DIR | Out-Null
    go build -o (Join-Path $BUILD_DIR $BINARY_NAME) ./cmd/main.go
    Write-Host "Built: $BUILD_DIR\$BINARY_NAME"
}

function Build-All {
    New-Item -ItemType Directory -Force -Path $BUILD_DIR | Out-Null
    foreach ($os in $GOOS_LIST) {
        foreach ($arch in $GOARCH_LIST) {
            $suffix = if ($os -eq "windows") { ".exe" } else { "" }
            $out = Join-Path $BUILD_DIR "$BINARY_NAME-$os-$arch$suffix"
            Write-Host "Building $os/$arch..."
            $env:GOOS = $os
            $env:GOARCH = $arch
            go build -o $out ./cmd/main.go
        }
    }
    Write-Host "Done. Binaries in $BUILD_DIR\"
}

function Clean-One {
    $f = Join-Path $BUILD_DIR $BINARY_NAME
    if (Test-Path $f) { Remove-Item $f -Force; Write-Host "Removed $f" }
}

function Clean-All {
    Clean-One
    foreach ($os in $GOOS_LIST) {
        foreach ($arch in $GOARCH_LIST) {
            $suffix = if ($os -eq "windows") { ".exe" } else { "" }
            $f = Join-Path $BUILD_DIR "$BINARY_NAME-$os-$arch$suffix"
            if (Test-Path $f) { Remove-Item $f -Force; Write-Host "Removed $f" }
        }
    }
}

switch ($Target) {
    "build"     { Build-One }
    "build-all" { Build-All }
    "clean"     { Clean-One }
    "clean-all" { Clean-All }
}
