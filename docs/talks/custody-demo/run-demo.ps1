[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:8127',
    [string]$Grant = $env:CUSTODY_DEMO_GRANT,
    [string]$StateDir = $env:CUSTODY_STATE
)

$ErrorActionPreference = 'Stop'

function Write-Beat {
    param([string]$Text)
    Write-Host "`n$Text" -ForegroundColor Cyan
}

function Assert-Status {
    param(
        [string]$Label,
        [int]$Actual,
        [int]$Expected
    )
    if ($Actual -ne $Expected) {
        throw "$Label returned HTTP $Actual; expected $Expected"
    }
    Write-Host "PASS  $Label -> HTTP $Actual" -ForegroundColor Green
}

function Invoke-Custody {
    param(
        [string]$Method,
        [string]$Path,
        [hashtable]$Headers = @{}
    )
    try {
        $response = Invoke-WebRequest -Method $Method -Uri "$BaseUrl$Path" -Headers $Headers -SkipHttpErrorCheck
        return $response
    } catch {
        throw "request $Method $Path failed before custody returned an HTTP response: $($_.Exception.Message)"
    }
}

Write-Beat '0. PRE-GRANT FLOOR — no credential or grant is required'
$unknown = Invoke-Custody -Method GET -Path '/not-a-key/rest/api/2/myself'
Assert-Status -Label 'unknown key is refused' -Actual $unknown.StatusCode -Expected 404

$missing = Invoke-Custody -Method GET -Path '/jira-microscope/rest/api/2/myself'
Assert-Status -Label 'missing grant is refused' -Actual $missing.StatusCode -Expected 401

$trace = Invoke-Custody -Method TRACE -Path '/jira-microscope/rest/api/2/myself'
Assert-Status -Label 'TRACE is refused before forwarding' -Actual $trace.StatusCode -Expected 405

if ([string]::IsNullOrWhiteSpace($Grant)) {
    Write-Host "`nCUSTODY_DEMO_GRANT is absent. Pre-grant checks passed; stopping at the operator-mint boundary." -ForegroundColor Yellow
    Write-Host 'After the operator stores the fake secret and mints a read grant, set CUSTODY_DEMO_GRANT and rerun.'
    exit 2
}

$grantHeaders = @{ 'X-Custody-Grant' = $Grant }

Write-Beat '1. INJECTION — the caller sends no Authorization header'
$injected = Invoke-Custody -Method GET -Path '/jira-microscope/rest/api/2/issue/DEMO-1?fields=id,status' -Headers $grantHeaders
Assert-Status -Label 'Jira-shaped read is forwarded' -Actual $injected.StatusCode -Expected 200
$injectedBody = $injected.Content | ConvertFrom-Json
if ($injectedBody.headers.Authorization -notlike 'Bearer *') {
    throw 'upstream did not receive the custody-injected Bearer header'
}
Write-Host 'PASS  upstream received a Bearer credential the caller did not send' -ForegroundColor Green
Write-Host "PASS  upstream path: $($injectedBody.path)" -ForegroundColor Green

Write-Beat '2. ISOLATION — caller-supplied identity is discarded and replaced'
$spoofHeaders = @{
    'X-Custody-Grant' = $Grant
    'Authorization' = 'Bearer AGENT-SUPPLIED-GARBAGE'
}
$isolated = Invoke-Custody -Method GET -Path '/jira-microscope/rest/api/2/myself' -Headers $spoofHeaders
Assert-Status -Label 'read with caller-supplied auth is forwarded' -Actual $isolated.StatusCode -Expected 200
$isolatedBody = $isolated.Content | ConvertFrom-Json
if ($isolatedBody.headers.Authorization -eq 'Bearer AGENT-SUPPLIED-GARBAGE') {
    throw 'caller-supplied Authorization reached the upstream'
}
if ($isolatedBody.headers.Authorization -notlike 'Bearer *') {
    throw 'custody did not replace the caller-supplied Authorization header'
}
Write-Host 'PASS  caller identity was dropped; custody injected its configured credential' -ForegroundColor Green

Write-Beat '3. SCOPE — the same grant passes reads and denies effects/broader paths'
$post = Invoke-Custody -Method POST -Path '/jira-microscope/rest/api/2/issue/DEMO-1' -Headers $grantHeaders
Assert-Status -Label 'write is denied at custody' -Actual $post.StatusCode -Expected 403

$wrongVersion = Invoke-Custody -Method GET -Path '/jira-microscope/rest/api/3/issue/DEMO-1' -Headers $grantHeaders
Assert-Status -Label 'wrong API version is denied at custody' -Actual $wrongVersion.StatusCode -Expected 403

$crossSurface = Invoke-Custody -Method GET -Path '/jira-microscope/wiki/rest/api/content/1' -Headers $grantHeaders
Assert-Status -Label 'cross-surface path is denied at custody' -Actual $crossSurface.StatusCode -Expected 403

if (-not [string]::IsNullOrWhiteSpace($StateDir)) {
    Write-Beat '4. AUDIT — summarize what custody allowed and refused'
    & custody log -state $StateDir -key jira-microscope -since 1h
    if ($LASTEXITCODE -ne 0) {
        throw "custody log exited $LASTEXITCODE"
    }
} else {
    Write-Host "`nCUSTODY_STATE is absent; skipping the log rollup." -ForegroundColor Yellow
}

Write-Host "`nDONE  The caller used a scoped capability; it never received the credential." -ForegroundColor Green
