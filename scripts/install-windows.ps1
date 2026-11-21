param(
    [switch]$SkipBuild,
    [switch]$Start,
    [switch]$AutoStart,
    [int]$Port = 8088,
    [string]$WorkspaceDir = ""
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $WorkspaceDir) {
    $WorkspaceDir = $RepoRoot
}

$InstallBinDir = Join-Path $env:LOCALAPPDATA "agy\bin"
$DataDir = Join-Path $env:LOCALAPPDATA "agy-gateway"
$LogDir = Join-Path $DataDir "logs"
$EnvFile = Join-Path $DataDir ".env"

New-Item -ItemType Directory -Force -Path $InstallBinDir, $DataDir, $LogDir | Out-Null

Write-Host "==> [1/4] Parando instancias antigas do AGY Gateway / Cockpit..." -ForegroundColor Cyan
Get-Process -Name "agy-gateway", "agy-cockpit" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
$ttydConn = Get-NetTCPConnection -LocalPort 7681 -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($ttydConn) {
    Stop-Process -Id $ttydConn.OwningProcess -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 400

if (-not $SkipBuild) {
    Write-Host "==> [2/4] Compilando frontend Svelte 5 e binarios Go..." -ForegroundColor Cyan
    $npmCmd = Get-Command npm -ErrorAction SilentlyContinue
    $distIndex = Join-Path $RepoRoot "backend-go\internal\web\static\dist\index.html"
    if ($npmCmd) {
        Push-Location (Join-Path $RepoRoot "frontend")
        try {
            if (-not (Test-Path "node_modules")) {
                npm install --no-audit --no-fund
                if ($LASTEXITCODE -ne 0) { throw "npm install falhou ($LASTEXITCODE)" }
            }
            npm run build
            if ($LASTEXITCODE -ne 0) { throw "npm run build falhou ($LASTEXITCODE)" }
        } finally {
            Pop-Location
        }
    } elseif (Test-Path $distIndex) {
        Write-Host "    Aviso: npm nao encontrado; usando bundle web pre-compilado do repositorio." -ForegroundColor Yellow
    } else {
        throw "Node.js/npm necessario para compilar o frontend na primeira vez."
    }

    $env:CGO_ENABLED = "0"
    Push-Location (Join-Path $RepoRoot "backend-go")
    try {
        go build -o agy-gateway.exe ./cmd/agy-gateway
        if ($LASTEXITCODE -ne 0) { throw "go build agy-gateway.exe falhou ($LASTEXITCODE)" }
    } finally {
        Pop-Location
    }

    Push-Location (Join-Path $RepoRoot "packages\cockpit")
    try {
        go build -o agy-cockpit.exe ./cmd/agy-cockpit
        if ($LASTEXITCODE -ne 0) { throw "go build agy-cockpit.exe falhou ($LASTEXITCODE)" }
    } finally {
        Pop-Location
    }
}

Write-Host "==> [3/4] Instalando binarios, .env e atalhos silenciosos em $InstallBinDir..." -ForegroundColor Cyan
$GatewaySrc = Join-Path $RepoRoot "backend-go\agy-gateway.exe"
$CockpitSrc = Join-Path $RepoRoot "packages\cockpit\agy-cockpit.exe"
$IconSrc = Join-Path $RepoRoot "backend-go\cmd\agy-gateway\app.ico"

Copy-Item -Path $GatewaySrc -Destination (Join-Path $InstallBinDir "agy-gateway.exe") -Force
if (Test-Path $CockpitSrc) {
    Copy-Item -Path $CockpitSrc -Destination (Join-Path $InstallBinDir "agy-cockpit.exe") -Force
}
if (Test-Path $IconSrc) {
    Copy-Item -Path $IconSrc -Destination (Join-Path $InstallBinDir "agy-gateway.ico") -Force
}

$ResolvedAgyBin = Join-Path $env:USERPROFILE ".gemini\bin\agy.exe"
if (-not (Test-Path $ResolvedAgyBin)) {
    $cmdAgy = Get-Command agy -ErrorAction SilentlyContinue
    if ($cmdAgy) { $ResolvedAgyBin = $cmdAgy.Source } else { $ResolvedAgyBin = "agy" }
}

if (-not (Test-Path $EnvFile)) {
    $rngBytes = New-Object byte[] 24
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($rngBytes)
    $CockpitToken = ([System.BitConverter]::ToString($rngBytes) -replace "-", "").ToLowerInvariant()
    $defaultEnv = @"
GATEWAY_MODE=standalone
PORT=$Port
ENABLE_TERMINAL=true
ENABLE_TELEMETRY=true
ENABLE_COCKPIT=true
ENABLE_OLLAMA=true
COCKPIT_ADDR=127.0.0.1:8099
COCKPIT_URL=http://127.0.0.1:8099
COCKPIT_ALLOW_POWER=false
COCKPIT_TOKEN=$CockpitToken
TTYD_WS_URL=ws://127.0.0.1:7681/terminal/ws
AGY_BIN=$ResolvedAgyBin
AGY_WORKSPACE=$WorkspaceDir
"@
    Set-Content -Path $EnvFile -Value $defaultEnv -Encoding UTF8
} else {
    $curEnv = Get-Content $EnvFile -Raw -ErrorAction SilentlyContinue
    if ($curEnv -and -not ($curEnv -match "(?m)^AGY_WORKSPACE=")) {
        Add-Content -Path $EnvFile -Value "`nAGY_WORKSPACE=$WorkspaceDir" -Encoding UTF8
    }
}

$InstallGatewayExe = Join-Path $InstallBinDir "agy-gateway.exe"
$DaemonScriptPath = Join-Path $InstallBinDir "start-agy-daemons.ps1"
if (Test-Path $DaemonScriptPath) { Remove-Item $DaemonScriptPath -Force -ErrorAction SilentlyContinue }

$StartupVbs = Join-Path $InstallBinDir "agy-startup.vbs"
$OpenVbs = Join-Path $InstallBinDir "agy-open.vbs"
if (Test-Path $OpenVbs) {
    Remove-Item $OpenVbs -Force -ErrorAction SilentlyContinue
}

$Wsh = New-Object -ComObject WScript.Shell
$StartupShortcutLegacy = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\Startup\AGY Mobile Gateway.lnk"
$TaskName = "AGY-Mobile-Gateway"

if ($AutoStart) {
    & $InstallGatewayExe --autostart on
    if ($LASTEXITCODE -ne 0) { throw "Falha ao ativar auto-start" }
} else {
    & $InstallGatewayExe --autostart off | Out-Null
    Write-Host "    Auto-start ao ligar/logar desativado (pode alternar no painel [7] a qualquer momento)." -ForegroundColor Green
}

$UserAppShortcut = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\AGY.lnk"
$DesktopDir = $Wsh.SpecialFolders("Desktop")
$DesktopShortcut = if ($DesktopDir) { Join-Path $DesktopDir "AGY.lnk" } else { "" }

foreach ($lnkPath in @($UserAppShortcut, $DesktopShortcut)) {
    if (-not $lnkPath) { continue }
    $sc = $Wsh.CreateShortcut($lnkPath)
    $sc.TargetPath = $InstallGatewayExe
    $sc.Arguments = "--console"
    $sc.WorkingDirectory = $WorkspaceDir
    if (Test-Path (Join-Path $InstallBinDir "agy-gateway.ico")) {
        $sc.IconLocation = (Join-Path $InstallBinDir "agy-gateway.ico")
    }
    $sc.Save()
}

$ProgramDataShortcut = "C:\ProgramData\Microsoft\Windows\Start Menu\AGY.lnk"
if (Test-Path $ProgramDataShortcut) {
    try {
        $scPub = $Wsh.CreateShortcut($ProgramDataShortcut)
        $scPub.TargetPath = $InstallGatewayExe
        $scPub.Arguments = "--console"
        $scPub.WorkingDirectory = $WorkspaceDir
        if (Test-Path (Join-Path $InstallBinDir "agy-gateway.ico")) {
            $scPub.IconLocation = (Join-Path $InstallBinDir "agy-gateway.ico")
        }
        $scPub.Save()
        Write-Host "    Atalho $ProgramDataShortcut atualizado para abrir o painel interativo." -ForegroundColor Green
    } catch {
        Write-Host "    Aviso: sem permissao de Admin para editar $ProgramDataShortcut (o .exe alvo ja abre o painel interativo diretamente)." -ForegroundColor Yellow
    }
}

if ($Start) {
    Write-Host "==> [4/4] Abrindo painel interativo do AGY..." -ForegroundColor Cyan
    Start-Process -FilePath $InstallGatewayExe -ArgumentList "--console" -WorkingDirectory $WorkspaceDir
}

Write-Host "==> Instalacao Windows concluida! Use o atalho AGY (Menu Iniciar ou Area de Trabalho) para controlar e ativar Auto-Start se quiser." -ForegroundColor Green
