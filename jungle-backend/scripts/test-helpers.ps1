# Verify process identity under different current cultures, without stopping it.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
$originalCulture = [Threading.Thread]::CurrentThread.CurrentCulture
$originalExe = $Stage8Exe
try {
    $self = Get-Process -Id $PID
    $Stage8Exe = $self.Path
    $entry = @{
        pid = $self.Id
        startedAtUtc = $self.StartTime.ToUniversalTime().ToString('o', [Globalization.CultureInfo]::InvariantCulture)
        startedAtTicks = $self.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
    }
    foreach ($culture in @('pt-BR','en-US','fr-FR')) {
        [Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::GetCultureInfo($culture)
        if (-not (Test-Stage8OwnedProcess $entry)) { throw "Valid identity rejected under $culture" }
        $wrong = $entry.Clone()
        $wrong.startedAtTicks = ([long]$entry.startedAtTicks + 1).ToString([Globalization.CultureInfo]::InvariantCulture)
        if (Test-Stage8OwnedProcess $wrong) { throw "Changed start time accepted under $culture" }
        $Stage8Exe = Join-Path $Stage8Root 'not-the-running-process.exe'
        if (Test-Stage8OwnedProcess $entry) { throw 'Different executable accepted.' }
        $Stage8Exe = $self.Path
    }
    Write-Host 'PASS: PID ownership and invariant timestamps under pt-BR, en-US and fr-FR.' -ForegroundColor Green
} finally {
    [Threading.Thread]::CurrentThread.CurrentCulture = $originalCulture
    $Stage8Exe = $originalExe
}
