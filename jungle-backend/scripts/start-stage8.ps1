# Launch independent Go executables; they share PostgreSQL/SQS but NOT memory.
param([int[]]$Ports)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$configuredPorts = @(Get-Stage8Ports)
if (-not $Ports) { $Ports = $configuredPorts }
if ($Ports | Where-Object { $_ -notin $configuredPorts }) { throw 'Ports deve ser um subconjunto de API_PORTS.' }
if (@($Ports | Select-Object -Unique).Count -ne $Ports.Count) { throw 'Portas repetidas no argumento Ports.' }
$previous = @(Get-Stage8Instances)
foreach ($port in $Ports) {
    if (@($previous | Where-Object { $_.port -eq $port }).Count -gt 0) { throw "Instancia $port ja registrada. Execute stop-stage8.ps1 -Ports $port antes." }
    Assert-Stage8PortFree $port
}
New-Item -ItemType Directory -Force -Path $Stage8BinDir, $Stage8RunDir | Out-Null
$oldAppAddr = $env:APP_ADDR
$new = @()
try {
    # Windows locks a running .exe, so when restoring a single failed instance
    # reuse the binary already running in the other two processes.
    if ($previous.Count -eq 0 -or -not (Test-Path $Stage8Exe)) {
        Push-Location $Stage8Root
        try {
            Write-Host 'Compilando o binario Go das instancias independentes...' -ForegroundColor Cyan
            go build -o $Stage8Exe ./cmd/api
            if ($LASTEXITCODE -ne 0) { throw "go build falhou ($LASTEXITCODE)" }
        } finally { Pop-Location }
    } else {
        foreach ($entry in $previous) {
            if (-not (Test-Stage8OwnedProcess $entry)) { throw 'Estado antigo de instancias inconsistente; verifique os processos antes de reiniciar.' }
        }
        Write-Host 'Reutilizando executavel das instancias ativas para nao reescrever um .exe bloqueado.' -ForegroundColor Cyan
    }
    foreach ($port in $Ports) {
        $env:APP_ADDR = "127.0.0.1:$port"
        $stdout = Join-Path $Stage8RunDir "api-$port.stdout.log"
        $stderr = Join-Path $Stage8RunDir "api-$port.stderr.log"
        $proc = Start-Process -FilePath $Stage8Exe -WorkingDirectory $Stage8Root -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        $entry = @{ port = $port; pid = $proc.Id; startedAtUtc = $proc.StartTime.ToUniversalTime().ToString('o', [Globalization.CultureInfo]::InvariantCulture); startedAtTicks = $proc.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture) }
        $new += $entry
        Save-Stage8Instances @($previous + $new)
        Write-Host "Processo $($proc.Id) iniciado na porta $port; aguardando readiness." -ForegroundColor Cyan
        try { Wait-Stage8Ready $port } catch {
            if (Test-Path $stderr) { Write-Host (Get-Content $stderr -Tail 18 | Out-String) -ForegroundColor Yellow }
            throw
        }
        Write-Host "PASS: instancia $port ready (PID $($proc.Id))" -ForegroundColor Green
    }
    Save-Stage8Instances @($previous + $new)
    Write-Host "STAGE 8: $($new.Count) instancia(s) iniciada(s). Registros em .stage8-run/instances.json" -ForegroundColor Green
} catch {
    foreach ($entry in $new) {
        if (Test-Stage8OwnedProcess $entry) { Stop-Process -Id $entry.pid -Force -ErrorAction SilentlyContinue }
    }
    Save-Stage8Instances $previous
    throw
} finally {
    $env:APP_ADDR = $oldAppAddr
}
