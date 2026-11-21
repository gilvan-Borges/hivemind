[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipTests,
    [switch]$RunCliTests,
    [switch]$SkipFrontendInstall,
    [switch]$Clean,
    # Assina os artefatos com signtool.exe (Authenticode). Exige o binario no
    # PATH (ou $env:HIVEMIND_SIGNTOOL) e um certificado via PFX ou thumbprint.
    [switch]$Sign
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$VersionFile = Join-Path $RepoRoot "VERSION"
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

function Invoke-GuiExe([string]$FilePath, [string[]]$ArgumentList) {
    $outTmp = [System.IO.Path]::GetTempFileName()
    $errTmp = [System.IO.Path]::GetTempFileName()
    try {
        $proc = Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -NoNewWindow -Wait -PassThru `
            -RedirectStandardOutput $outTmp -RedirectStandardError $errTmp
        $stdout = if (Test-Path $outTmp) { (Get-Content $outTmp -Raw -ErrorAction SilentlyContinue) } else { "" }
        $stderr = if (Test-Path $errTmp) { (Get-Content $errTmp -Raw -ErrorAction SilentlyContinue) } else { "" }
        $combined = (($stdout, $stderr) | Where-Object { $_ -and $_.Trim() -ne "" }) -join "`n"
        return @{
            ExitCode = $proc.ExitCode
            Output   = $combined.Trim()
        }
    }
    finally {
        Remove-Item $outTmp, $errTmp -Force -ErrorAction SilentlyContinue
    }
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

function Find-SignTool {
    $explicit = $env:HIVEMIND_SIGNTOOL
    if ($explicit -and (Test-Path $explicit)) { return $explicit }
    $cmd = Get-Command "signtool" -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kits = @(
        "C:\Program Files (x86)\Windows Kits\10\bin",
        "C:\Program Files\Windows Kits\10\bin"
    )
    foreach ($kit in $kits) {
        if (-not (Test-Path $kit)) { continue }
        # Pega a versao mais nova de x64 disponivel.
        $newest = Get-ChildItem $kit -Directory | Sort-Object Name -Descending |
            ForEach-Object { Join-Path $_.FullName "x64\signtool.exe" } |
            Where-Object { Test-Path $_ } | Select-Object -First 1
        if ($newest) { return $newest }
    }
    return $null
}

# Versao unica de verdade: arquivo VERSION na raiz do repositorio
# (docs/specs/10-mobile-companion-supply-chain-and-ci.md, secao 3.2.1).
function Resolve-AppVersion([string]$Explicit) {
    if ($Explicit) { return $Explicit.TrimStart("v") }
    if (-not (Test-Path $VersionFile)) {
        throw "Arquivo VERSION nao encontrado na raiz ($VersionFile). Ele e a fonte unica de versao."
    }
    $fromFile = (Get-Content $VersionFile -Raw).Trim()
    if (-not $fromFile) { throw "Arquivo VERSION vazio." }
    return $fromFile
}

function Get-CommitSha {
    try {
        return (git -C $RepoRoot rev-parse --short=8 HEAD).Trim()
    } catch {
        return "unknown"
    }
}

function Get-FileSha256([string]$Path) {
    return (Get-FileHash $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Injecao -ldflags compartilhada: os tres binarios reportam exatamente a mesma
# versao/commit/data (internal/version e a fonte unica no Go).
function VersionLdflags([string]$AppVersion) {
    $pkg = "github.com/gilvan-Borges/HiveMind/internal/version"
    $commit = Get-CommitSha
    $date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    return "-X ${pkg}.Version=$AppVersion -X ${pkg}.Commit=$commit -X ${pkg}.BuildDate=$date"
}

# O Inno Setup e o wails.json nao leem o VERSION: o script confere que estao em
# sincronia antes de construir, para nao gerar instalador com versao errada.
function Assert-WailsJsonVersion([string]$AppVersion) {
    $wailsJson = Join-Path $IdeDir "wails.json"
    if (-not (Test-Path $wailsJson)) { return }
    $json = Get-Content $wailsJson -Raw | ConvertFrom-Json
    if ($json.info.productVersion -and [string]$json.info.productVersion -ne $AppVersion) {
        throw "ide/wails.json productVersion=[$($json.info.productVersion)] diverge do VERSION=[$AppVersion]. Atualize ide/wails.json antes de construir."
    }
}

# Assinatura Authenticode condicional: so roda com -Sign; sem certificado
# configurado o build falha em vez de assinar por engano (fail closed).
function Invoke-SignTool([string]$SignToolExe, [string]$Target) {
    $args = @("sign", "/fd", "SHA256", "/td", "SHA256")

    $pfx = $env:HIVEMIND_SIGN_PFX
    $pfxPassword = $env:HIVEMIND_SIGN_PFX_PASSWORD
    $thumbprint = $env:HIVEMIND_SIGN_THUMBPRINT
    if ($pfx) {
        if (-not $pfxPassword) {
            throw "HIVEMIND_SIGN_PFX definido sem HIVEMIND_SIGN_PFX_PASSWORD."
        }
        $args += @("/f", $pfx, "/p", $pfxPassword)
    } elseif ($thumbprint) {
        $args += @("/sha1", $thumbprint)
    } else {
        throw "Assinatura pedida (-Sign) sem certificado: defina HIVEMIND_SIGN_PFX+HIVEMIND_SIGN_PFX_PASSWORD ou HIVEMIND_SIGN_THUMBPRINT."
    }

    $timestamp = $env:HIVEMIND_TIMESTAMP_URL
    if (-not $timestamp) { $timestamp = "http://timestamp.digicert.com" }
    $args += @("/tr", $timestamp)
    $args += $Target

    & $SignToolExe @args
    if ($LASTEXITCODE -ne 0) {
        throw "signtool falhou ao assinar $Target (exit $LASTEXITCODE). O build nao continua sem assinatura valida."
    }
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
    $Ldflags = VersionLdflags $AppVersion
    Assert-WailsJsonVersion $AppVersion

    Write-Host "Go:         $(& $goExe version)"
    Write-Host "Node:       $(& $nodeExe --version)"
    Write-Host "Wails:      $wailsExe"
    Write-Host "Inno Setup: $isccExe"
    Write-Host "Versao:     $AppVersion (ldflags: $Ldflags)"

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
        & $goExe build -trimpath -ldflags "-s -w $Ldflags" -o $cliExe .\cmd\hivemind
        if ($LASTEXITCODE -ne 0) { throw "Falha ao compilar hivemind.exe" }
    }
    finally {
        $env:CGO_ENABLED = $prevCgo
        $env:GOOS = $prevGoos
        $env:GOARCH = $prevGoarch
    }
    # Smoke-check: a versao impressa precisa ser a mesma do VERSION/ldflags.
    $printed = (& $cliExe version) -join " "
    Write-Host "hivemind version -> $printed"
    if (-not $printed.Contains($AppVersion)) {
        throw "hivemind version impresso [$printed] nao contem a versao $AppVersion; a injecao de ldflags falhou."
    }

    Write-Step "Instalando dependencias do frontend da IDE (ide\frontend)"
    Push-Location $FrontendDir
    try {
        if (-not $SkipFrontendInstall) {
            # npm ci e obrigatorio: builds reproduziveis a partir do lockfile.
            # Sem package-lock.json o build falha em vez de resolver uma versao qualquer.
            if (-not (Test-Path "package-lock.json")) {
                throw "package-lock.json ausente em ide\frontend. Gere-o (npm install --package-lock-only) e comite; o build exige npm ci."
            }
            & $npmExe ci
            if ($LASTEXITCODE -ne 0) { throw "npm ci falhou no frontend da IDE" }
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
        $logoPng = Join-Path $RepoRoot "assets\brand\appicon.png"
        $logoIco = Join-Path $RepoRoot "assets\brand\hivemind.ico"
        $wailsBuildDir = Join-Path $IdeDir "build"
        $wailsWinDir = Join-Path $wailsBuildDir "windows"
        New-Item -ItemType Directory -Force -Path $wailsWinDir | Out-Null
        if (Test-Path $logoPng) { Copy-Item $logoPng (Join-Path $wailsBuildDir "appicon.png") -Force }
        if (Test-Path $logoIco) { Copy-Item $logoIco (Join-Path $wailsWinDir "icon.ico") -Force }

        Write-Step "Compilando HiveMind IDE com Wails (bindings + frontend + Go)"
        # Destino unico: ide\build\bin\hivemind-ide.exe. Nao existe mais copia
        # manual divergente em ide\build\ (Spec 10, secao 3.2.1).
        & $wailsExe build -clean -trimpath -platform windows/amd64 -ldflags "-s -w $Ldflags"
        if ($LASTEXITCODE -ne 0) { throw "wails build falhou" }

        $ideExe = Join-Path $IdeDir "build\bin\hivemind-ide.exe"
        if (-not (Test-Path $ideExe)) {
            throw "Executavel da IDE nao encontrado em $ideExe"
        }

        Write-Step "Executando smoke-check da IDE"
        $smokeRes = Invoke-GuiExe $ideExe @("--smoke-check")
        if ($smokeRes.Output) { Write-Host $smokeRes.Output }
        if ($smokeRes.ExitCode -ne 0) { throw "Smoke-check da IDE falhou (exit $($smokeRes.ExitCode))" }
        $verRes = Invoke-GuiExe $ideExe @("--version")
        $ideVersion = ($verRes.Output -replace "\r?\n", " ").Trim()
        Write-Host "hivemind-ide --version -> $ideVersion"
        if ($verRes.ExitCode -ne 0 -or -not $ideVersion.Contains($AppVersion)) {
            throw "hivemind-ide --version impresso [$ideVersion] nao contem a versao $AppVersion."
        }
    }
    finally {
        Pop-Location
    }

    $ideExeFinal = Join-Path $IdeDir "build\bin\hivemind-ide.exe"
    $iconPath = Join-Path $IdeDir "build\windows\icon.ico"
    if (-not (Test-Path $iconPath)) {
        throw "Icone nao encontrado em $iconPath"
    }

    if ($Sign) {
        Write-Step "Assinando binarios e instalador (signtool Authenticode)"
        $signTool = Find-SignTool
        if (-not $signTool) {
            throw "-Sign pedido mas signtool.exe nao encontrado (PATH, Windows Kits ou HIVEMIND_SIGNTOOL)."
        }
        Invoke-SignTool $signTool $cliExe
        Invoke-SignTool $signTool $ideExeFinal
    } else {
        Write-Host "Assinatura Authenticode desativada (use -Sign para assinar com signtool)." -ForegroundColor Yellow
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

    if ($Sign) {
        Invoke-SignTool (Find-SignTool) $setupExe
    }

    Write-Step "Gerando manifesto e checksums SHA-256 (dist\checksums.txt + dist\manifest.json)"
    # manifest.json usa chaves semanticas (Role) para o updater pedir "installer"
    # sem conhecer o nome de arquivo, que muda por versao/OS. checksums.txt usa
    # o nome real de cada arquivo (formato sha256sum -c).
    $artifactPaths = @(
        @{ Role = "installer"; Name = "HiveMindSetup-x64.exe"; Path = $setupExe },
        @{ Role = "cli-windows"; Name = "hivemind.exe"; Path = $cliExe },
        @{ Role = "ide-windows"; Name = "hivemind-ide.exe"; Path = $ideExeFinal }
    )
    $checksumLines = New-Object System.Collections.Generic.List[string]
    $checksumLines.Add("# HiveMind $AppVersion - SHA-256 por artefato (sha256sum -c compativel)")
    $manifestArtifacts = @{}
    foreach ($art in $artifactPaths) {
        if (-not (Test-Path $art.Path)) { continue }
        $hash = Get-FileSha256 $art.Path
        $size = (Get-Item $art.Path).Length
        $checksumLines.Add("$hash  $($art.Name)")
        $manifestArtifacts[$art.Role] = @{
            name = $art.Name
            sha256 = $hash
            size = $size
        }
    }
    $checksumsPath = Join-Path $DistDir "checksums.txt"
    Set-Content -Path $checksumsPath -Value $checksumLines -Encoding ascii

    $manifestPath = Join-Path $DistDir "manifest.json"
    $commit = Get-CommitSha
    $buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $manifest = @{
        version = $AppVersion
        commit = $commit
        buildDate = $buildDate
        generator = "scripts/build-installer.ps1"
        artifacts = $manifestArtifacts
    }
    ($manifest | ConvertTo-Json -Depth 4) | Set-Content -Path $manifestPath -Encoding ascii

    # O proprio VERSION acompanha o release: o updater e o instalador confiam nele.
    Copy-Item $VersionFile (Join-Path $DistDir "VERSION") -Force

    $info = Get-Item $setupExe
    $hash = (Get-FileHash $setupExe -Algorithm SHA256).Hash
    Write-Step "Build concluido com sucesso!"
    Write-Host ("Instalador: {0} ({1:N2} MB)" -f $setupExe, ($info.Length / 1MB)) -ForegroundColor Green
    Write-Host ("SHA-256:    {0}" -f $hash) -ForegroundColor Green
    Write-Host ("Checksums:  {0}" -f $checksumsPath) -ForegroundColor Green
    Write-Host ("Manifesto:  {0}" -f $manifestPath) -ForegroundColor Green
    Write-Host ("CLI avulso: {0}" -f $cliExe)
    Write-Host ("IDE avulsa: {0}" -f $ideExeFinal)
}
finally {
    Pop-Location
}
