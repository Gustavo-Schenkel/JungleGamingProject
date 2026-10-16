# Stop ONLY previously started, identity-verified Stage 8 processes.
param([int[]]$Ports)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
$all = @(Get-Stage8Instances)
if (-not $Ports) { $Ports = @($all | ForEach-Object { [int]$_.port }) }
if ($all.Count -eq 0) { Write-Host 'Nao existem instancias Stage 8 registradas.'; return }
$remain = @()
foreach ($entry in $all) {
    if ([int]$entry.port -notin $Ports) { $remain += $entry; continue }
    if (Test-Stage8OwnedProcess $entry) {
        Stop-Process -Id ([int]$entry.pid) -Force -ErrorAction Stop
        for ($i = 0; $i -lt 25; $i++) {
            if (-not (Get-Process -Id ([int]$entry.pid) -ErrorAction SilentlyContinue)) { break }
            Start-Sleep -Milliseconds 200
        }
        if (Get-Process -Id ([int]$entry.pid) -ErrorAction SilentlyContinue) {
            throw "Processo $($entry.pid) nao encerrou; nao e seguro reiniciar a porta $($entry.port)."
        }
        Write-Host "Encerrada instancia porta $($entry.port) PID $($entry.pid)" -ForegroundColor Yellow
    } else {
        if (Get-Process -Id ([int]$entry.pid) -ErrorAction SilentlyContinue) {
            throw "PID $($entry.pid) pertence a outro executavel/processo; parada recusada por seguranca."
        }
        Write-Host "Instancia $($entry.port) ja estava encerrada." -ForegroundColor Yellow
    }
}
Save-Stage8Instances $remain
Write-Host "Instancias restantes: $($remain.Count)." -ForegroundColor Green
