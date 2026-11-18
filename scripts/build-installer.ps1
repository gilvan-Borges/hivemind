[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipTests,
    [switch]$RunCliTests,
    [switch]$SkipFrontendInstall,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$DistDir = Join-Path $RepoRoot "dist"
$IdeDir = Join-Path $RepoRoot "ide"
$FrontendDir = Join-Path $IdeDir "frontend"
$IssPath = Join-Path $PSScriptRoot "installer\hivemind.iss"

function Write-Step([string]$Message) {
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Require-Command([string]$Name, [string]$Hint) {
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $cmd) {
        throw "Ferramenta '$Name' nao encontrada no PATH. $Hint"
    }
    return $cmd.Source
}

function Find-Iscc {
    $cmd = Get-Command "iscc" -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    $candidates = @(
        (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "C:\Program Files\Inno Setup 6\ISCC.exe"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) { return $candidate }
    }
    throw "Inno Setup 6 (ISCC.exe) nao encontrado. Instale via: winget install --id JRSoftware.InnoSetup -e"
}

function Resolve-AppVersion([string]$Explicit) {
    if ($Explicit) { return $Explicit.TrimStart("v") }
    $rootGo = Join-Path $RepoRoot "internal\cli\root.go"
    if (Test-Path $rootGo) {
        $match = Select-String -Path $rootGo -Pattern 'var Version = "([^"]+)"' | Select-Object -First 1
        if ($match) {
            return $match.Matches[0].Groups[1].Value
        }
    }
    return "0.1.0"
}

Push-Location $RepoRoot
try {
    Write-Step "Verificando ferramentas locais"
    $goExe = Require-Command "go" "Instale Go 1.26.10+"
    $nodeExe = Require-Command "node" "Instale Node.js 20+"
    $npmExe = Require-Command "npm" "Instale Node.js/npm"
    $wailsExe = Require-Command "wails" "Instale com: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    $isccExe = Find-Iscc

    $AppVersion = Resolve-AppVersion $Version
    Write-Host "Go:         $(& $goExe version)"
    Write-Host "Node:       $(& $nodeExe --version)"
    Write-Host "Wails:      $wailsExe"
    Write-Host "Inno Setup: $isccExe"
    Write-Host "Versao:     $AppVersion"

    if ($Clean) {
        Write-Step "Limpando artefatos anteriores"
        if (Test-Path $DistDir) { Remove-Item -Recurse -Force $DistDir }
        $ideBin = Join-Path $IdeDir "build\bin"
        if (Test-Path $ideBin) { Remove-Item -Recurse -Force $ideBin }
    }

    New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

    if (-not $SkipTests) {
        Write-Step "Validando CLI (go vet)"
        & $goExe vet ./...
        if ($LASTEXITCODE -ne 0) { throw "go vet falhou no CLI" }
        if ($RunCliTests) {
            Write-Step "Rodando testes unitarios do CLI (go test ./...)"
            & $goExe test ./...
            if ($LASTEXITCODE -ne 0) { throw "go test falhou no CLI" }
        }
    }

    Write-Step "Compilando binario da CLI (dist\hivemind.exe)"
    $cliExe = Join-Path $DistDir "hivemind.exe"
    $prevCgo = $env:CGO_ENABLED
    $prevGoos = $env:GOOS
    $prevGoarch = $env:GOARCH
    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = "windows"
        $env:GOARCH = "amd64"
        & $goExe build -trimpath -ldflags "-s -w -X github.com/gilvan-Borges/HiveMind/internal/cli.Version=$AppVersion" -o $cliExe .\cmd\hivemind
        if ($LASTEXITCODE -ne 0) { throw "Falha ao compilar hivemind.exe" }
    }
    finally {
        $env:CGO_ENABLED = $prevCgo
        $env:GOOS = $prevGoos
        $env:GOARCH = $prevGoarch
    }
    & $cliExe version
    if ($LASTEXITCODE -ne 0) { throw "Smoke-check da CLI falhou" }

    Write-Step "Instalando dependencias do frontend da IDE (ide\frontend)"
    Push-Location $FrontendDir
    try {
        if (-not $SkipFrontendInstall) {
            if (Test-Path "package-lock.json") {
                & $npmExe ci
            } else {
                & $npmExe install
            }
            if ($LASTEXITCODE -ne 0) { throw "Falha ao instalar dependencias do frontend" }
        }
    }
    finally {
        Pop-Location
    }

    Push-Location $IdeDir
    try {
        if (-not $SkipTests) {
            Write-Step "Validando core da IDE (go vet e go test)"
            & $goExe vet ./internal/idecore/...
            if ($LASTEXITCODE -ne 0) { throw "go vet falhou no idecore" }
            & $goExe test ./internal/idecore/...
            if ($LASTEXITCODE -ne 0) { throw "go test falhou no idecore" }
        }

        Write-Step "Sincronizando logo HiveMind para Wails"
        $logoPng = Join-Path $RepoRoot "assets\hivemind-logo.png"
        $logoIco = Join-Path $RepoRoot "assets\hivemind.ico"
        $wailsBuildDir = Join-Path $IdeDir "build"
        $wailsWinDir = Join-Path $wailsBuildDir "windows"
        New-Item -ItemType Directory -Force -Path $wailsWinDir | Out-Null
        if (Test-Path $logoPng) { Copy-Item $logoPng (Join-Path $wailsBuildDir "appicon.png") -Force }
        if (Test-Path $logoIco) { Copy-Item $logoIco (Join-Path $wailsWinDir "icon.ico") -Force }

        Write-Step "Compilando HiveMind IDE com Wails (bindings + frontend + Go)"
        & $wailsExe build -clean -trimpath -platform windows/amd64
        if ($LASTEXITCODE -ne 0) { throw "wails build falhou" }

        $ideExe = Join-Path $IdeDir "build\bin\hivemind-ide.exe"
        if (-not (Test-Path $ideExe)) {
            throw "Executavel da IDE nao encontrado em $ideExe"
        }

        Write-Step "Executando smoke-check da IDE"
        & $ideExe --smoke-check
        if ($LASTEXITCODE -ne 0) { throw "Smoke-check da IDE falhou" }
    }
    finally {
        Pop-Location
    }

    $ideExeFinal = Join-Path $IdeDir "build\bin\hivemind-ide.exe"
    $iconPath = Join-Path $IdeDir "build\windows\icon.ico"
    if (-not (Test-Path $iconPath)) {
        throw "Icone nao encontrado em $iconPath"
    }

    Write-Step "Gerando instalador Windows (.exe) via Inno Setup"
    & $isccExe `
        "/DAppVersion=$AppVersion" `
        "/DRepoRoot=$RepoRoot" `
        "/DIdeExePath=$ideExeFinal" `
        "/DCliExePath=$cliExe" `
        "/DIconPath=$iconPath" `
        "/DOutputDir=$DistDir" `
        $IssPath
    if ($LASTEXITCODE -ne 0) { throw "ISCC falhou ao gerar o instalador" }

    $setupExe = Join-Path $DistDir "HiveMindSetup-x64.exe"
    if (-not (Test-Path $setupExe)) {
        throw "Instalador esperado nao foi gerado em $setupExe"
    }

    $info = Get-Item $setupExe
    $hash = (Get-FileHash $setupExe -Algorithm SHA256).Hash
    Write-Step "Build concluido com sucesso!"
    Write-Host ("Instalador: {0} ({1:N2} MB)" -f $setupExe, ($info.Length / 1MB)) -ForegroundColor Green
    Write-Host ("SHA-256:    {0}" -f $hash)
    Write-Host ("CLI avulso: {0}" -f $cliExe)
    Write-Host ("IDE avulsa: {0}" -f $ideExeFinal)
}
finally {
    Pop-Location
}
