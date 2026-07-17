# floor-score — the floor+advisory combined scorer, SHIPPING semantics (spec §3/§6/§7):
#   - the signature floor decides first (table below = the seed set the spec ships:
#     ETIMEDOUT|ECONNREFUSED demoted to advisory-only per §7);
#   - on floor abstain, the advisory bucket counts ONLY if the verbatim-evidence
#     verifier trusts it (non-empty normalized substring of the input + enum bucket);
#   - a distrusted advisory row ESCALATES — it is neither correct nor wrong, it is
#     unenriched (§6). Metrics: coverage (handled fraction), accuracy ON handled,
#     escalate rate, and every trusted-wrong row by direction.
# History: v1 of this scorer accepted advisory buckets without the verifier and kept
# the demoted signature — the cycle-2 panel caught both (codex P1/P2). The advertised
# numbers moved from "92.2% / 96.1% of all rows" to the §1 two-axis table.
param(
  [string]$s = $PSScriptRoot,
  [string]$raw = 'ci-eval-raw.14b.jsonl'
)
function norm($x){ if(-not $x){return ''}; (($x.ToString().ToLower() -replace '[^a-z0-9]',' ') -split '\s+' | Where-Object {$_}) -join ' ' }

# Seed signature table (spec §7). First match wins, most specific first. The floor
# claims only flake/infra; real-break is the advisory's job.
$sig = @(
  @{re='(?i)the database system is (starting up|shutting down)'; b='flake'},
  @{re='(?i)connection to server on socket .*(failed|no such file)'; b='flake'},
  @{re='(?i)\bEBUSY\b|resource busy or locked'; b='flake'},
  @{re='(?i)EADDRINUSE|address already in use|port .* already in use'; b='flake'},
  @{re='(?i)passed on retry|retried and passed|passe[sd] on rerun|attempt \d+; passed'; b='flake'},
  @{re='(?i)failed to authenticate'; b='infra'},
  @{re='(?i)workflow initiated by non.?human actor'; b='infra'},
  @{re='(?i)429 too many requests'; b='infra'},
  @{re='(?i)could not resolve host'; b='infra'},
  @{re='(?i)no space left on device'; b='infra'},
  @{re='(?i)received a shutdown signal and disconnected'; b='infra'},
  @{re='(?i)go version file .* does not exist'; b='infra'},
  @{re='(?i)/installation/token'; b='infra'}
)
# Wrapper/teardown exclusion (spec §7): a signature only fires on a line that is
# NOT a generic wrapper line and NOT inside a step's teardown region (everything
# after the first "Post job cleanup." line of that step section).
$wrapRe = '(?i)(ELIFECYCLE|ERR_PNPM_RECURSIVE|make: \*\*\*|Process completed with exit code|Command failed with exit code|exit status |npm error code|waiting for other jobs|Post job cleanup|Cleaning up orphan|Terminate orphan process|docker (rm|network rm)|Stop and remove container)'
function floorOf($text){
  foreach($section in ($text -split '(?m)^=== failed step:')){
    $teardown = $false
    foreach($line in ($section -split "`n")){
      if($line -match '(?i)Post job cleanup'){ $teardown = $true }
      if($teardown){ continue }
      if($line -match $wrapRe){ continue }
      foreach($x in $sig){ if($line -match $x.re){ return $x.b } }
    }
  }
  return $null
}

$data = Get-Content (Join-Path $s 'ci-lines-v2.jsonl') | Where-Object {$_.Trim()} | ForEach-Object { $_ | ConvertFrom-Json }
$out  = Get-Content (Join-Path $s $raw)                | Where-Object {$_.Trim()} | ForEach-Object { $_ | ConvertFrom-Json }
if ($data.Count -ne $out.Count) { throw "row mismatch: data=$($data.Count) out=$($out.Count) — refusing to score a prefix" }

$enum=@('flake','real-break','infra')
$n=$data.Count
$floorFired=0;$floorOK=0;$trusted=0;$trustedOK=0;$esc=0
$wrong=@();$escRows=@()
for($i=0;$i -lt $n;$i++){
  $exp=$data[$i].expected; $in=$data[$i].input; $o=$out[$i].output
  $fb=floorOf $in
  if($fb){
    $floorFired++
    if($fb -eq $exp){$floorOK++} else {$wrong+=[pscustomobject]@{src='floor';dir="$exp -> $fb";meta=$data[$i].meta}}
    continue
  }
  $mb = if($o){"$($o.bucket)"}else{''}
  $ev = if($o){norm $o.evidence}else{''}
  $trust = ($ev -ne '') -and ((norm $in).Contains($ev)) -and ($enum -contains $mb)
  if(-not $trust){
    $esc++
    $why = if(-not $o){'no output'}elseif($ev -eq ''){'empty evidence'}elseif(-not ((norm $in).Contains($ev))){'non-verbatim'}else{'bad bucket'}
    $escRows+=[pscustomobject]@{exp=$exp;mb=$mb;why=$why;meta=$data[$i].meta}
    continue
  }
  $trusted++
  if($mb -eq $exp){$trustedOK++} else {$wrong+=[pscustomobject]@{src='advisory';dir="$exp -> $mb";meta=$data[$i].meta}}
}
$handled=$floorFired+$trusted; $handledOK=$floorOK+$trustedOK
Write-Output "===== FLOOR + ADVISORY, shipping semantics ($raw, $n rows) ====="
Write-Output ("floor fired      : {0}  precision {1}/{0} = {2:P0}" -f $floorFired,$floorOK,$(if($floorFired){$floorOK/$floorFired}else{0}))
Write-Output ("advisory trusted : {0}  accuracy {1}/{0} = {2:P0}" -f $trusted,$trustedOK,$(if($trusted){$trustedOK/$trusted}else{0}))
Write-Output ("escalated        : {0} = {1:P0} of rows (unenriched, safe by construction)" -f $esc,($esc/$n))
Write-Output ("COVERAGE         : {0}/{1} = {2:P1} handled locally   (bar >= 60%)" -f $handled,$n,($handled/$n))
Write-Output ("ON-HANDLED ACC   : {0}/{1} = {2:P1}                    (bar >= 90%)" -f $handledOK,$handled,$(if($handled){$handledOK/$handled}else{0}))
Write-Output ''
Write-Output '--- trusted-wrong rows (the only ones that can mislead a consumer) ---'
if($wrong){ $wrong | Group-Object dir | Sort-Object Count -Descending | ForEach-Object { Write-Output ("[{0}] {1}" -f $_.Count,$_.Name); foreach($m in $_.Group){ Write-Output ("      via {0}: {1}" -f $m.src,$m.meta) } } } else { Write-Output '(none)' }
Write-Output '--- escalated rows ---'
$escRows | ForEach-Object { Write-Output ("  exp={0,-10} model={1,-10} {2,-14} {3}" -f $_.exp,$_.mb,$_.why,$_.meta) }
