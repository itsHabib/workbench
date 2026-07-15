#Requires -Version 5.1
<#
.SYNOPSIS
  Manage the flare-watch scheduled task (workbench's escalation watcher).

.DESCRIPTION
  One entry point for flare's unattended-operation lifecycle on Windows.
  It encodes the two things that are easy to get wrong by hand:

    - creating/removing a scheduled task needs an elevated (admin) token, so
      `install` and `uninstall` self-elevate via one UAC prompt;
    - Windows locks a running .exe, so `update` must STOP the task before
      `go install` can overwrite the binary, then START it again.

  `update`, `restart`, and `status` run unprivileged.

  This is machine-wiring that lives beside flare, never inside it: flare stays
  a pure sink and cannot overwrite its own running binary anyway.

.PARAMETER Command
  install   register + start the task (self-elevates)
  update    stop -> go install ./cmd/flare -> start (picks up new flare code)
  restart   restart the task (reload routes.json after an edit)
  status    task state + `flare status`
  uninstall stop + unregister the task (self-elevates)

.EXAMPLE
  .\flare-task.ps1 install
.EXAMPLE
  .\flare-task.ps1 update
.EXAMPLE
  .\flare-task.ps1 status
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'update', 'restart', 'status', 'uninstall')]
    [string]$Command = 'status'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# flare status exits 1 when stale and `go install` may exit non-zero; neither
# should throw here -- we branch on $LASTEXITCODE explicitly instead.
$PSNativeCommandUseErrorActionPreference = $false

$TaskName = 'flare-watch'
# scripts/ -> flare/ -> cmd/ -> repo root
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Relaunch this script elevated for $Command, then exit the unprivileged instance.
function Invoke-SelfElevate {
    Write-Host "'$Command' needs admin -- relaunching elevated (accept the UAC prompt)..." -ForegroundColor Yellow
    $hostExe = (Get-Process -Id $PID).Path
    $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-NoExit',
                 '-File', "`"$PSCommandPath`"", $Command)
    try {
        Start-Process -FilePath $hostExe -Verb RunAs -ArgumentList $argList
    } catch {
        Write-Warning "Elevation cancelled -- '$Command' not performed."
        exit 1
    }
    exit 0
}

function Resolve-FlareExe {
    $cmd = Get-Command flare -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $fallback = Join-Path $env:USERPROFILE 'go\bin\flare.exe'
    if (Test-Path $fallback) { return $fallback }
    throw "flare binary not found on PATH or at $fallback -- run '.\flare-task.ps1 update' or 'go install ./cmd/flare' first."
}

function Get-FlareTask {
    Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

function Register-FlareTask {
    $exe = Resolve-FlareExe
    Write-Host "flare binary: $exe"
    # Run headless (no console window). A visible console exits 0xC000013A when
    # closed, silently killing the daemon; conhost --headless (Windows 11) gives
    # the console app a window-less host so nothing can Ctrl+C it.
    $action = New-ScheduledTaskAction -Execute 'conhost.exe' -Argument "--headless `"$exe`" watch"
    $trigger = New-ScheduledTaskTrigger -AtLogOn
    $settings = New-ScheduledTaskSettingsSet -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew `
        -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero)
    if (Get-FlareTask) {
        Write-Host "Re-registering existing '$TaskName'..."
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    }
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
        -Settings $settings `
        -Description 'flare escalation watcher (workbench) - poll loop, pushes block/escalate to Slack/toast' | Out-Null
    Write-Host "Registered '$TaskName'." -ForegroundColor Green
}

# Stop the task, then wait for the flare process to actually exit so it releases
# the .exe lock before `go install` tries to overwrite it.
function Stop-FlareAndWait {
    param([string]$Exe)
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $deadline) {
        $proc = Get-Process -Name flare -ErrorAction SilentlyContinue |
                Where-Object { $_.Path -eq $Exe }
        if (-not $proc) { return }
        Start-Sleep -Milliseconds 300
    }
    Write-Warning "flare still running after 15s; 'go install' may fail to overwrite $Exe"
}

function Show-Status {
    $task = Get-FlareTask
    if (-not $task) {
        Write-Warning "'$TaskName' is not installed. Run: .\flare-task.ps1 install"
        return
    }
    $task | Select-Object TaskName, State | Format-List
    Get-ScheduledTaskInfo -TaskName $TaskName |
        Format-List TaskName, LastRunTime, LastTaskResult, NumberOfMissedRuns, NextRunTime
    Write-Host '--- flare status (exit 1 == stale; informational) ---'
    & (Resolve-FlareExe) status
}

switch ($Command) {
    'install' {
        if (-not (Test-Admin)) { Invoke-SelfElevate }
        Register-FlareTask
        Start-ScheduledTask -TaskName $TaskName
        Start-Sleep -Seconds 3
        Show-Status
    }
    'uninstall' {
        if (-not (Test-Admin)) { Invoke-SelfElevate }
        if (-not (Get-FlareTask)) {
            Write-Host "'$TaskName' is not installed; nothing to do."
            break
        }
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
        Write-Host "Unregistered '$TaskName'." -ForegroundColor Green
    }
    'update' {
        if (-not (Get-FlareTask)) { throw "'$TaskName' not installed. Run: .\flare-task.ps1 install" }
        $exe = Resolve-FlareExe
        Write-Host "Stopping '$TaskName' to release $exe ..."
        Stop-FlareAndWait -Exe $exe
        Write-Host "Rebuilding flare from $RepoRoot ..."
        $buildOk = $false
        Push-Location $RepoRoot
        try {
            & go install ./cmd/flare
            $buildOk = ($LASTEXITCODE -eq 0)
        } finally {
            Pop-Location
            Start-ScheduledTask -TaskName $TaskName   # always bring flare back up
        }
        if (-not $buildOk) { throw "go install failed -- restarted the previous binary." }
        Write-Host 'Update complete (new binary running).' -ForegroundColor Green
        Start-Sleep -Seconds 2
        Show-Status
    }
    'restart' {
        if (-not (Get-FlareTask)) { throw "'$TaskName' not installed. Run: .\flare-task.ps1 install" }
        Restart-ScheduledTask -TaskName $TaskName
        Write-Host "Restarted '$TaskName' (config reloaded)." -ForegroundColor Green
        Start-Sleep -Seconds 2
        Show-Status
    }
    'status' {
        Show-Status
    }
}
