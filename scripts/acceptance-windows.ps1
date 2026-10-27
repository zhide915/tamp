<#
.SYNOPSIS
Runs tamp end to end against Docker Desktop on Windows.

.DESCRIPTION
Builds an environment with a site and an app, checks that Mutagen syncs apps/
both ways, measures the setup-time targets, and with -Agent runs the agent
test. Any failed check or missed target makes the script exit 1.

The script runs tamp under a temp home, so it never reads or writes your own
~/.tamp. It needs Docker Desktop running, port 80 free, host git, and no
tamp-router container or running tamp environment. It removes everything it
makes, except the bench image and the tamp-cache volume.

Run it from a normal PowerShell prompt in the repository root:

    powershell -ExecutionPolicy Bypass -File scripts\acceptance-windows.ps1 -Agent
#>
[CmdletBinding()]
param(
    # The binary under test, such as a release download. Built from this checkout when omitted.
    [string]$Tamp,
    # Fail unless the first tamp new builds the bench image, which measures the first-environment target.
    # Remove the version-15 tamp-bench image first, with `docker rmi`.
    [switch]$FirstEnvironment,
    # Run the agent test with the Claude Code CLI, `claude`.
    [switch]$Agent
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8

$repo = Split-Path -Parent $PSScriptRoot
$realHome = $env:USERPROFILE
$work = Join-Path ([IO.Path]::GetTempPath()) ("tamp-acceptance-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$tempHome = Join-Path $work 'home'
$envs = Join-Path $work 'envs'
$envName = 'tamp-accept'
$site = 'accept.localhost'
$envDir = Join-Path $envs $envName
$made = New-Object System.Collections.Generic.List[string]
$timings = New-Object System.Collections.Generic.List[string]
$browserCheck = $null
$saved = @{ USERPROFILE = $env:USERPROFILE; HOME = $env:HOME; DOCKER_CONFIG = $env:DOCKER_CONFIG; PATH = $env:PATH; TEMP = $env:TEMP; TMP = $env:TMP }

function Say([string]$Text) { Write-Host "`n=== $Text" -ForegroundColor Cyan }

function Fail([string]$Text) { throw "acceptance: $Text" }

# Native runs a program, echoes its output unless -Quiet, and returns the exit code and output.
# Stderr is merged as text: under 'Stop', Windows PowerShell turns it into errors.
function Native {
    param([string]$File, [string[]]$Arguments, [switch]$Quiet)
    $ErrorActionPreference = 'Continue'
    $lines = @(& $File @Arguments 2>&1 | ForEach-Object {
            # A stderr line arrives as an error record, which prints a type name for an empty line.
            $line = if ($_ -is [Management.Automation.ErrorRecord]) { "$($_.TargetObject)" } else { "$_" }
            if (-not $Quiet) { Write-Host $line }
            $line
        })
    [pscustomobject]@{ Code = $LASTEXITCODE; Out = ($lines -join "`n") }
}

function T { Native $script:tampExe $args }
function TQuiet { Native $script:tampExe $args -Quiet }

function Must([object]$Result, [string]$What) {
    if ($Result.Code -ne 0) { Fail "$What exited $($Result.Code)" }
}

# Target records a timing, and fails when it misses its target.
function Target([string]$What, [int]$Limit, [Diagnostics.Stopwatch]$Watch) {
    $took = [int]$Watch.Elapsed.TotalSeconds
    $line = "- ${What}: $took s, target $Limit s or less"
    $script:timings.Add($line)
    Write-Host "timing $line"
    if ($took -gt $Limit) { Fail "$What took $took s, over its target of $Limit s" }
}

function Wait-Until([int]$Seconds, [scriptblock]$Check) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Check) { return $true }
        Start-Sleep -Seconds 2
    }
    return $false
}

# Through-Router fetches with curl through the router on port 80, whatever *.localhost resolves to here.
function Through-Router([string]$HostName, [string[]]$Arguments) {
    Native 'curl.exe' (@('-sS', '--max-time', '10', '--resolve', "${HostName}:80:127.0.0.1") + $Arguments) -Quiet
}

function Pongs([string]$HostName) {
    Wait-Until 60 { (Through-Router $HostName @("http://$HostName/api/method/ping")).Out -match 'pong' }
}

function Bench-Images {
    ((Native 'docker' @('images', '--quiet', '--filter', 'reference=tamp-bench:*') -Quiet).Out -split "`n" | Sort-Object) -join ' '
}

function Asset-Requests {
    ([regex]::Matches((TQuiet logs frappe).Out, '"GET /assets/\S+\.js\S* HTTP/[\d.]+" (200|304)')).Count
}

# GitBash is Git for Windows' bash, as plain `bash` may be WSL's.
function GitBash {
    $root = Split-Path (Split-Path (Split-Path ((git --exec-path) -replace '/', '\')))
    $bash = Join-Path $root 'usr\bin\bash.exe'
    if (-not (Test-Path $bash)) { Fail "no bash at $bash, which Git for Windows installs" }
    $bash
}

function Cleanup {
    Set-Location $repo
    foreach ($name in $made) { TQuiet remove -e $name --yes --quiet | Out-Null }
    # tamp leaves Mutagen's daemon running, and it holds the temp home open.
    $mutagen = Get-ChildItem -Path (Join-Path $tempHome '.tamp\bin') -Recurse -Filter 'mutagen.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($mutagen) {
        $env:MUTAGEN_DATA_DIRECTORY = Join-Path $tempHome '.tamp\mutagen'
        Native $mutagen.FullName @('daemon', 'stop') -Quiet | Out-Null
        Remove-Item Env:\MUTAGEN_DATA_DIRECTORY
    }
    # tamp never removes the router. The pre-checks made sure this run created it, and its network.
    Native 'docker' @('rm', '--force', 'tamp-router') -Quiet | Out-Null
    if (-not $networkExisted) { Native 'docker' @('network', 'rm', 'tamp-router_default') -Quiet | Out-Null }
    foreach ($k in $saved.Keys) { Set-Item "Env:\$k" $saved[$k] -ErrorAction SilentlyContinue }
    if (-not $saved.HOME) { Remove-Item Env:\HOME -ErrorAction SilentlyContinue }
    if (-not $saved.DOCKER_CONFIG) { Remove-Item Env:\DOCKER_CONFIG -ErrorAction SilentlyContinue }
    for ($i = 0; $i -lt 30 -and (Test-Path $work); $i++) {
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
        if (Test-Path $work) { Start-Sleep -Seconds 2 }
    }
    if (Test-Path $work) { Write-Host "acceptance: could not delete $work" -ForegroundColor Yellow }
}

# The pre-checks run before the cleanup is armed, so a refusal touches nothing.
Say 'the machine has Docker, no tamp-router, and no running tamp environment'
$refusal = $null
if ((Native 'docker' @('info', '--format', '{{.ServerVersion}}')).Code -ne 0) {
    $refusal = 'docker info failed: start Docker Desktop, and run the script again'
} elseif ((Native 'docker' @('ps', '--all', '--quiet', '--filter', 'name=^tamp-router$') -Quiet).Out) {
    $refusal = 'a tamp-router container exists, which the script would share: remove it with `docker rm --force tamp-router`, ' +
    'and run the script again; your next `tamp start` creates it again'
} elseif ((Native 'docker' @('ps', '--quiet', '--filter', 'label=tamp.env') -Quiet).Out) {
    $refusal = 'a tamp environment is running, which the router would serve too: find it with `docker ps --filter label=tamp.env`, ' +
    'stop it with `tamp stop -e <name>`, and run the script again'
} elseif ($leftEnv = @('tamp-accept', 'tamp-accept2') |
        Where-Object { (Native 'docker' @('ps', '--all', '--quiet', '--filter', "label=tamp.env=$_") -Quiet).Out } | Select-Object -First 1) {
    $refusal = "the environment $leftEnv exists, which the cleanup would remove: remove it with ``tamp remove -e $leftEnv --yes``, and run the script again"
} elseif ($leftVolumes = @((Native 'docker' @('volume', 'ls', '--quiet', '--filter', 'name=^tamp-accept2?_(db|bench)$') -Quiet).Out -split "`n" |
            Where-Object { $_ }) -join ' ') {
    # tamp remove finds an environment by its containers, so it cannot remove volumes a failed tamp new left alone.
    $refusal = "the volumes $leftVolumes, which this run would adopt and its cleanup remove, are left from a failed tamp new: " +
    "remove them with ``docker volume rm $leftVolumes``, and run the script again"
} elseif ($Agent -and -not (Get-Command claude -ErrorAction SilentlyContinue)) {
    $refusal = 'no claude on PATH: install Claude Code, or run without -Agent'
}
if ($refusal) {
    Write-Host "acceptance: $refusal" -ForegroundColor Red
    exit 1
}
$networkExisted = [bool](Native 'docker' @('network', 'ls', '--quiet', '--filter', 'name=^tamp-router_default$') -Quiet).Out

$failed = $null
try {
    New-Item -ItemType Directory -Path (Join-Path $work 'bin'), $tempHome, $envs | Out-Null
    $script:tampExe = Join-Path $work 'bin\tamp.exe'
    if ($Tamp) {
        Copy-Item $Tamp $tampExe
    } else {
        Say "building tamp from $repo"
        Push-Location $repo
        try { Must (Native 'go' @('build', '-o', $tampExe, './cmd/tamp')) 'go build' } finally { Pop-Location }
    }
    $bash = GitBash

    # Docker's config stays the user's, for its contexts and the Compose plugin.
    if (-not $env:DOCKER_CONFIG) { $env:DOCKER_CONFIG = Join-Path $realHome '.docker' }
    $env:USERPROFILE = $tempHome
    $env:HOME = $tempHome
    # Agents and the printed examples call tamp by name.
    $env:PATH = (Join-Path $work 'bin') + ';' + $env:PATH
    Set-Location $envs

    Say 'tamp version prints the version, commit, and build date'
    $out = T version
    Must $out 'tamp version'
    foreach ($p in @('(?m)^tamp ', '(?m)^commit: ', '(?m)^build date: ')) { if ($out.Out -notmatch $p) { Fail "tamp version lacks $p" } }

    Say 'tamp new builds the environment, and the timing meets its target'
    $before = Bench-Images
    $watch = [Diagnostics.Stopwatch]::StartNew()
    # Recorded first, so the cleanup removes a half-made environment.
    $made.Add($envName)
    Must (T new $envName --frappe version-15 --site $site) 'tamp new'
    $watch.Stop()
    $built = (Bench-Images) -ne $before
    if ($built) {
        Target 'first environment on the machine (image built)' 900 $watch
    } elseif ($FirstEnvironment) {
        Fail 'tamp new found the bench image cached, so -FirstEnvironment measured nothing: remove the image with `docker rmi`, and run again'
    } else {
        Target 'new environment, image cached' 480 $watch
        $timings.Add('- first environment on the machine (image built): not measured, as the image was cached; run with -FirstEnvironment')
    }

    if ($built) {
        Say 'a second environment with the image cached meets its target'
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $made.Add('tamp-accept2')
        Must (T new tamp-accept2 --frappe version-15 --site accept2.localhost) 'the second tamp new'
        $watch.Stop()
        Target 'new environment, image cached' 480 $watch
        if (-not (Pongs 'accept2.localhost')) { Fail 'accept2.localhost did not answer through the router' }
        Must (T remove -e tamp-accept2 --yes) 'tamp remove of the second environment'
    }

    Set-Location $envDir

    Say 'the site answers through the router'
    if (-not (Pongs $site)) { Fail "$site did not answer through the router" }

    Say 'tamp app add clones a local app and installs it'
    $remote = Join-Path $work 'remotes\acme-erp.git'
    Must (Native $bash @((Join-Path $repo 'scripts\minimal-app.sh'), ($remote -replace '\\', '/'))) 'minimal-app.sh'
    Must (T app add ('file:///' + ($remote -replace '\\', '/'))) 'tamp app add'
    if (-not (Test-Path (Join-Path $envDir 'apps\acme\acme\hooks.py'))) { Fail 'apps\acme holds no clone' }

    Say 'apps/ syncs through Mutagen, not a bind mount'
    $mounts = (Native 'docker' @('inspect', '--format', '{{ range .Mounts }}{{ .Type }}:{{ .Destination }} {{ end }}', "$envName-frappe-1") -Quiet).Out
    if ($mounts -match 'bind:/home/frappe/frappe-bench/apps') { Fail "apps/ is bind-mounted: $mounts" }
    if (-not (Get-ChildItem -Path (Join-Path $tempHome '.tamp\bin') -Recurse -Filter 'mutagen.exe')) { Fail 'tamp did not download Mutagen' }
    if (-not (Wait-Until 60 { (TQuiet status).Out -match '(?m)^sync: watching' })) { Fail "the sync session is not watching: $((TQuiet status).Out)" }
    Must (T doctor) 'tamp doctor'

    Say 'a file written on the host reaches the container'
    Set-Content -Path (Join-Path $envDir 'apps\acme\from_host.txt') -Value 'host' -Encoding ascii
    if (-not (Wait-Until 30 { $r = TQuiet shell '--' cat apps/acme/from_host.txt; $r.Code -eq 0 -and $r.Out.Trim() -eq 'host' })) { Fail 'from_host.txt did not reach the container' }

    Say 'a file written in the container reaches the host'
    Must (T shell '--' sh -c 'echo container > apps/acme/from_container.txt') 'tamp shell'
    $back = Join-Path $envDir 'apps\acme\from_container.txt'
    if (-not (Wait-Until 30 { Test-Path $back })) { Fail 'from_container.txt did not reach the host' }

    Say 'a file deleted on the host is deleted in the container'
    Remove-Item (Join-Path $envDir 'apps\acme\from_host.txt'), $back
    if (-not (Wait-Until 30 { $r = TQuiet shell '--' ls apps/acme; $r.Code -eq 0 -and $r.Out -notmatch 'from_' })) { Fail 'the deletions did not reach the container' }

    Say 'tamp start on a stopped environment meets its target'
    Must (T stop) 'tamp stop'
    $watch = [Diagnostics.Stopwatch]::StartNew()
    Must (T start) 'tamp start'
    $watch.Stop()
    Target 'start on an existing environment' 60 $watch
    if (-not (Pongs $site)) { Fail "$site did not answer after stop and start" }
    if (-not (Wait-Until 60 { (TQuiet status).Out -match '(?m)^sync: watching' })) { Fail 'the sync session is not watching after start' }

    if ($Agent) {
        Say 'the agent test: an agent given only "run tamp context" makes a change, tests it, and checks it'
        $prompt = 'Run `tamp context`. Then make this change in the app acme: add a whitelisted method ' +
        'acme.api.greet that returns the text Hello from tamp, add a test for it and run that test, then check the change in the browser.'
        $transcript = Join-Path $work 'agent.jsonl'
        # The agent keeps the user's Claude Code sign-in, but none of its MCP servers.
        $env:CLAUDE_CONFIG_DIR = Join-Path $realHome '.claude'
        # The agent's scratch files, such as a browser profile, go where the cleanup deletes them.
        $agentTemp = Join-Path $work 'agent-temp'
        New-Item -ItemType Directory -Path $agentTemp | Out-Null
        $env:TEMP = $agentTemp
        $env:TMP = $agentTemp
        $tempBefore = @(Get-ChildItem -Force -Name $saved.TEMP)
        $assetsBefore = Asset-Requests
        try {
            $run = Native 'claude' @('-p', $prompt, '--output-format', 'stream-json', '--verbose', '--strict-mcp-config',
                '--allowedTools', 'Bash,PowerShell,Read,Edit,Write,Glob,Grep') -Quiet
        } finally {
            Remove-Item Env:\CLAUDE_CONFIG_DIR
            $env:TEMP = $saved.TEMP
            $env:TMP = $saved.TMP
        }
        # An agent may still name the real temp folder, where other programs write too, so the script only lists what appeared.
        $left = @(Get-ChildItem -Force -Name $saved.TEMP | Where-Object { $tempBefore -notcontains $_ })
        if ($left) { Write-Host "Check and delete what appeared in $($saved.TEMP) while the agent ran: $($left -join ', ')" -ForegroundColor Yellow }
        Set-Content -Path $transcript -Value $run.Out -Encoding utf8
        # Claude Code on Windows runs commands through its PowerShell tool, or its Bash tool in Git Bash.
        $commands = New-Object System.Collections.Generic.List[object]
        $result = ''
        $denied = ''
        foreach ($line in ($run.Out -split "`n")) {
            if (-not $line.StartsWith('{')) { continue }
            $ev = $line | ConvertFrom-Json
            if ($ev.type -eq 'result') {
                $result = $ev.result
                $denied = ($ev.permission_denials | ForEach-Object { $_.tool_name }) -join ', '
            }
            if ($ev.type -ne 'assistant') { continue }
            foreach ($c in $ev.message.content) {
                if ($c.type -eq 'tool_use' -and $c.name -in @('Bash', 'PowerShell')) {
                    $commands.Add([pscustomobject]@{ Tool = $c.name; Command = $c.input.command })
                }
            }
        }
        Write-Host 'The agent ran:'
        $commands | ForEach-Object { Write-Host "  [$($_.Tool)] $($_.Command)" }
        Write-Host "The agent said: $result"
        if ($denied) { Write-Host "Denied tools: $denied" }
        if ($run.Code -ne 0) { Fail "claude exited $($run.Code)" }
        if (-not ($commands | Where-Object { $_.Command -match 'tamp context' })) { Fail 'the agent never ran tamp context' }
        $tests = @($commands | Where-Object { $_.Command -match 'tamp shell' -and $_.Command -match 'run-tests' })
        if (-not $tests) { Fail 'the agent never ran a test through tamp shell' }
        # The web server logs each request, so this holds for curl and a browser alike.
        if ((TQuiet logs frappe).Out -notmatch '"(GET|POST) /api/method/acme\.api\.greet\S* HTTP/[\d.]+" 200') {
            Fail 'the agent never fetched acme.api.greet from the site: the frappe logs show no request that answered 200'
        }
        # A browser that renders a page fetches its JavaScript, which curl of a method never does.
        $assets = (Asset-Requests) - $assetsBefore
        if ($assets -gt 0) {
            $script:browserCheck = "detected: the frappe logs show $assets JavaScript asset requests while the agent ran"
        } else {
            $script:browserCheck = 'not detected: no page loaded its JavaScript while the agent ran, so "in the browser" is unproven for this run'
        }
        Write-Host "Browser check: $browserCheck"

        Say "the agent's test module passes on the test site"
        # The module, not the agent's last command, which may break the code on purpose to see the test fail.
        $modules = @($tests | ForEach-Object { [regex]::Matches($_.Command, 'run-tests\b[^;&|\r\n]*?--module[=\s]+["'']?([\w.]+)') } |
            ForEach-Object { $_.Groups[1].Value } | Select-Object -Unique)
        if (-not $modules) { Fail 'the agent ran no run-tests --module' }
        foreach ($module in $modules) {
            Must (T shell '--' env CI=1 bench --site "$envName-test.localhost" run-tests --module $module) "run-tests --module $module"
        }

        Say 'the change answers through the router for a logged-in user'
        $jar = Join-Path $work 'cookies'
        Must (Through-Router $site @('-c', $jar, '-d', 'usr=Administrator', '-d', 'pwd=password', "http://$site/api/method/login")) 'the login'
        $greet = Through-Router $site @('-b', $jar, "http://$site/api/method/acme.api.greet")
        Write-Host $greet.Out
        if ($greet.Out -notmatch 'Hello from tamp') { Fail "acme.api.greet answered: $($greet.Out)" }
        if (-not (Test-Path (Join-Path $envDir 'apps\acme\acme\api.py'))) { Fail 'the change is not on the host, in apps\acme\acme\api.py' }
    }

    Say 'acceptance passed'
} catch {
    $failed = $_
} finally {
    Cleanup
}

Write-Host "`n=== timings"
$timings | ForEach-Object { Write-Host $_ }
if ($browserCheck) { Write-Host "`n=== the agent's browser check: $browserCheck" }
if ($failed) {
    Write-Host "$failed" -ForegroundColor Red
    exit 1
}
