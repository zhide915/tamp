<#
Release acceptance for Windows: the checks CI cannot run, because GitHub's
Windows runners cannot run Linux containers. Run before every tagged
release, from a normal (non-elevated) prompt with Docker Desktop running:

    powershell -ExecutionPolicy Bypass -File scripts\acceptance-windows.ps1
#>
[CmdletBinding()]
param(
    # The binary under test; built from this checkout when omitted.
    [string]$Tamp,
    # Where the throwaway environment goes; must not exist yet.
    [string]$WorkDir = (Join-Path $env:TEMP "tamp-acceptance")
)

$ErrorActionPreference = "Stop"
$results = [ordered]@{}

function Record([string]$Name, [bool]$Ok) {
    $results[$Name] = $Ok
    if ($Ok) { Write-Host "  PASS  $Name" -ForegroundColor Green }
    else { Write-Host "  FAIL  $Name" -ForegroundColor Red }
}

function Confirm-Step([string]$Question) {
    while ($true) {
        $answer = Read-Host "$Question [y/n]"
        if ($answer -eq "y") { return $true }
        if ($answer -eq "n") { return $false }
    }
}

# Remove-TampBlock is everything the hosts file holds that is not tamp's -
# what must survive a sync byte for byte.
function Remove-TampBlock([string]$Text) {
    $pattern = '(?ms)^# --- tamp managed block ---\r?\n.*?^# --- end tamp block ---\r?\n'
    return [regex]::Replace($Text, $pattern, "")
}

function Get-StatusCode([string]$HostName, [string]$Path, [int]$Port) {
    # Retries: the bench's first request may still be importing apps.
    $code = "000"
    for ($i = 0; $i -lt 30; $i++) {
        $code = & curl.exe -s -o NUL -w "%{http_code}" `
            -H "Host: $HostName" "http://127.0.0.1:$Port$Path"
        if ($code -eq "200") { return $code }
        Start-Sleep -Seconds 4
    }
    return $code
}

# --- Elevation-free operation -----------------------------------------------
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if ($identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "This prompt is elevated. tamp must work without elevation, so the"
    Write-Host "acceptance run must happen from a normal prompt. Start one and rerun."
    exit 1
}
Record "runs without elevation" $true

if (Test-Path $WorkDir) {
    Write-Host "$WorkDir already exists - remove it (or pass -WorkDir) and rerun."
    exit 1
}

if (-not $Tamp) {
    $repo = Split-Path -Parent $PSScriptRoot
    $Tamp = Join-Path $env:TEMP "tamp-acceptance-tamp.exe"
    Write-Host "Building tamp from $repo"
    Push-Location $repo
    try { go build -o $Tamp ./cmd/tamp } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { Write-Host "go build failed"; exit 1 }
}
New-Item -ItemType Directory -Path $WorkDir | Out-Null

$envName = "acceptance"
$siteHost = "$envName.localhost"
$password = -join ((48..57) + (97..122) | Get-Random -Count 16 | ForEach-Object { [char]$_ })

# --- Doctor -----------------------------------------------------------------
Write-Host "`n=== tamp doctor" -ForegroundColor Cyan
& $Tamp doctor
Record "tamp doctor is clean" ($LASTEXITCODE -eq 0)

# --- Create, with Mutagen (the Windows default) -----------------------------
Write-Host "`n=== create ($envName, version-15, erpnext pinned) - takes a while" -ForegroundColor Cyan
& $Tamp create $envName --frappe version-15 --apps erpnext:version-15 --dir $WorkDir
Record "create succeeds" ($LASTEXITCODE -eq 0)
if ($LASTEXITCODE -ne 0) {
    Write-Host "Create failed - nothing further can run. Not safe to release."
    exit 1
}
# A blocked Mutagen falls back to a bind mount with exit 0; only the
# compose file records the mode.
$compose = Join-Path $WorkDir "$envName\compose.yaml"
$usedBind = Select-String -Path $compose -Pattern "\./apps:" -Quiet
Record "create used Mutagen, not the bind-mount fallback" (-not $usedBind)

# --- Site -------------------------------------------------------------------
Write-Host "`n=== site new" -ForegroundColor Cyan
& $Tamp site new $envName $siteHost --admin-password $password
Record "site new succeeds" ($LASTEXITCODE -eq 0)

$stateFile = Join-Path $HOME ".tamp\router\router.json"
$port = 80
if (Test-Path $stateFile) {
    $recorded = (Get-Content $stateFile -Raw | ConvertFrom-Json).port
    if ($recorded) { $port = $recorded }
}

Record "site answers 200 through the router" `
    ((Get-StatusCode $siteHost "/api/method/ping" $port) -eq "200")
Record "mail UI answers 200 through the router" `
    ((Get-StatusCode "mail.$siteHost" "/" $port) -eq "200")

# --- Browser + hot reload ---------------------------------------------------
$url = "http://${siteHost}"
if ($port -ne 80) { $url = "http://${siteHost}:$port" }
Write-Host "`n=== hot reload" -ForegroundColor Cyan
Write-Host "Opening $url/app - log in as Administrator with password: $password"
Start-Process "$url/app"
Record "desk loads and login works" (Confirm-Step "Does the desk load and the login work?")

$deskJs = Join-Path $WorkDir "$envName\apps\frappe\frappe\public\js\frappe\desk.js"
$original = [IO.File]::ReadAllBytes($deskJs)
# A Ctrl+C at either prompt must not leave the marker in the frappe checkout.
try {
    Write-Host "Keep the desk visible. On Enter, this script edits a Frappe source"
    Write-Host "file on the HOST; the browser must reload itself - no manual refresh."
    Read-Host "Press Enter to make the edit" | Out-Null
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    [IO.File]::AppendAllText($deskJs, "`n// tamp acceptance $(Get-Date -Format o)`n")
    Read-Host "Press Enter the moment the browser reloads (or when you give up)" | Out-Null
    $stopwatch.Stop()
    $seconds = [Math]::Round($stopwatch.Elapsed.TotalSeconds, 1)
    Write-Host "Measured: $seconds seconds (edit on host -> Mutagen -> bench watch -> reload)"
    Record "browser reflects a host-side edit in <5s" `
        (Confirm-Step "Did the browser reload ITSELF within about 5 seconds?")
} finally {
    [IO.File]::WriteAllBytes($deskJs, $original)
}

# --- Mutagen, container-to-host direction -----------------------------------
Write-Host "`n=== sync back from the container" -ForegroundColor Cyan
& $Tamp exec $envName -- touch apps/frappe/tamp-acceptance-marker
$marker = Join-Path $WorkDir "$envName\apps\frappe\tamp-acceptance-marker"
$synced = $false
for ($i = 0; $i -lt 30; $i++) {
    if (Test-Path $marker) { $synced = $true; break }
    Start-Sleep -Seconds 1
}
Record "a file created in the container reaches the host" $synced
& $Tamp exec $envName -- rm -f apps/frappe/tamp-acceptance-marker

# --- Snapshots --------------------------------------------------------------
Write-Host "`n=== snapshot create / restore" -ForegroundColor Cyan
& $Tamp snapshot create $envName --name acceptance
Record "snapshot create succeeds" ($LASTEXITCODE -eq 0)
$bundle = Join-Path $WorkDir "$envName\.tamp\snapshots\acceptance.tar.gz"
Record "the snapshot bundle reached the host" (Test-Path $bundle)
Record "the bundle is not empty" ((Test-Path $bundle) -and ((Get-Item $bundle).Length -gt 0))

& $Tamp clean $envName --data --yes
Record "clean --data succeeds" ($LASTEXITCODE -eq 0)
& $Tamp snapshot restore $envName --name acceptance
Record "snapshot restore succeeds" ($LASTEXITCODE -eq 0)
Record "the restored site answers 200 through the router" `
    ((Get-StatusCode $siteHost "/api/method/ping" $port) -eq "200")

# --- Custom domain and the one elevated operation ---------------------------
Write-Host "`n=== hosts sync" -ForegroundColor Cyan
$customHost = "acceptance.tamp.test"
$hostsFile = Join-Path $env:SystemRoot "System32\drivers\etc\hosts"
$hostsBefore = [IO.File]::ReadAllText($hostsFile)

& $Tamp site new $envName $customHost --admin-password $password
Record "site new on a custom domain succeeds" ($LASTEXITCODE -eq 0)
$listing = & $Tamp site list $envName | Out-String
Record "site list marks the hosts entry pending" `
    ($listing -match ([regex]::Escape($customHost) + ".*pending"))

Write-Host "tamp is about to ask Windows for elevation. Approve the UAC prompt."
Read-Host "Press Enter, then approve the prompt" | Out-Null
& $Tamp hosts sync
Record "hosts sync succeeds" ($LASTEXITCODE -eq 0)
Record "the sync asked for elevation, for the write alone" `
    (Confirm-Step "Did exactly one UAC prompt appear, and only now?")
Record "no earlier tamp command asked for elevation" `
    (Confirm-Step "Was that the first UAC prompt of the whole run?")

$hostsAfter = [IO.File]::ReadAllText($hostsFile)
Record "the entry landed inside tamp's marked block" `
    ($hostsAfter -match "(?m)^# --- tamp managed block ---$")
Record "nothing outside the block changed" `
    ((Remove-TampBlock $hostsAfter) -eq $hostsBefore)
Record "the custom domain resolves to the router" `
    ((Get-StatusCode $customHost "/api/method/ping" $port) -eq "200")

& $Tamp site rm $envName $customHost --yes
Write-Host "tamp is about to ask for elevation again, to take the line out."
Read-Host "Press Enter, then approve the prompt" | Out-Null
& $Tamp hosts sync
Record "removing the site takes its line out on the next sync" `
    ([IO.File]::ReadAllText($hostsFile) -eq $hostsBefore)

# --- rm keeps the source ----------------------------------------------------
Write-Host "`n=== rm" -ForegroundColor Cyan
& $Tamp rm $envName --yes
Record "rm succeeds" ($LASTEXITCODE -eq 0)
Record "rm leaves the source tree intact" `
    (Test-Path (Join-Path $WorkDir "$envName\apps\frappe"))
Write-Host "The environment directory is kept at $WorkDir for inspection;"
Write-Host "delete it yourself once you are done."

# --- Verdict ----------------------------------------------------------------
Write-Host "`n=== verdict" -ForegroundColor Cyan
$failed = 0
foreach ($entry in $results.GetEnumerator()) {
    $mark = "PASS"
    if (-not $entry.Value) { $mark = "FAIL"; $failed++ }
    Write-Host ("  {0}  {1}" -f $mark, $entry.Key)
}
if ($failed -eq 0) {
    Write-Host "`nSAFE TO RELEASE" -ForegroundColor Green
    exit 0
}
Write-Host "`nNOT safe to release: $failed check(s) failed" -ForegroundColor Red
exit 1
