# Scores the ci-classifier eval: bucket accuracy, verbatim rate, confusion
# matrix, and the misdirection audit (every miss by direction, with the two
# costly ones called out). Joins the -jsonl model output back to the dataset
# by row index (eval processes rows in order) to recover each input for the
# verbatim check.
param(
  [string]$s = $PSScriptRoot,
  [string]$raw = 'ci-eval-raw.7b.jsonl'
)
function norm($x){ if(-not $x){return ''}; (($x.ToString().ToLower() -replace '[^a-z0-9]',' ') -split '\s+' | Where-Object {$_}) -join ' ' }

$data = Get-Content (Join-Path $s 'ci-lines-v2.jsonl') | Where-Object {$_.Trim()} | ForEach-Object { $_ | ConvertFrom-Json }
$out  = Get-Content (Join-Path $s $raw) | Where-Object {$_.Trim()} | ForEach-Object { $_ | ConvertFrom-Json }
if ($data.Count -ne $out.Count) { throw "row mismatch: data=$($data.Count) out=$($out.Count) — refusing to score a prefix" }

$buckets = @('flake','real-break','infra')
$conf = @{}; foreach($e in $buckets){ foreach($p in $buckets+@('ERR')){ $conf["$e>$p"]=0 } }
$correct=0; $verb=0; $verbApplicable=0; $lowConf=0; $misses=@(); $n=$data.Count  # counts validated equal by the throw above
for($i=0;$i -lt $n;$i++){
  $exp=$data[$i].expected
  $o=$out[$i].output
  $pred = if($o){ "$($o.bucket)" } else { 'ERR' }
  $conf["$exp>$pred"]++
  if($pred -eq $exp){ $correct++ }
  else { $misses += [pscustomobject]@{ dir="$exp -> $pred"; meta=$data[$i].meta; conf=$(if($o){$o.confidence}else{'-'}); evidence=$(if($o){$o.evidence}else{$out[$i].error}) } }
  if($o){
    $verbApplicable++
    $ni = norm $data[$i].input; $ne = norm $o.evidence
    if($ne -ne '' -and $ni.Contains($ne)){ $verb++ }
    if($o.confidence -ne $null -and [double]$o.confidence -lt 0.7){ $lowConf++ }
  }
}
$acc=[math]::Round($correct/$n,4)
$vrate=[math]::Round($verb/[Math]::Max(1,$verbApplicable),4)
Write-Output "===== CI-CLASSIFIER EVAL ($n rows) ====="
Write-Output ("bucket accuracy : {0}/{1} = {2:P1}" -f $correct,$n,$acc)
Write-Output ("verbatim(evidence): {0}/{1} = {2:P1}" -f $verb,$verbApplicable,$vrate)
Write-Output ("low-confidence(<0.70): {0}/{1}  (these self-flag for escalation)" -f $lowConf,$n)
Write-Output ("plan bar >= 0.90 bucket accuracy  ->  {0}" -f $(if($acc -ge 0.9){'GO'}else{'NO-GO'}))
Write-Output ''
Write-Output '--- confusion matrix (rows=truth, cols=predicted) ---'
Write-Output ("{0,-12} {1,8} {2,12} {3,8} {4,6}" -f 'truth\pred','flake','real-break','infra','ERR')
foreach($e in $buckets){
  Write-Output ("{0,-12} {1,8} {2,12} {3,8} {4,6}" -f $e,$conf["$e>flake"],$conf["$e>real-break"],$conf["$e>infra"],$conf["$e>ERR"])
}
Write-Output ''
Write-Output '--- per-bucket recall ---'
foreach($e in $buckets){ $tot=($buckets+@('ERR')|ForEach-Object{$conf["$e>$_"]}|Measure-Object -Sum).Sum; $hit=$conf["$e>$e"]; Write-Output ("{0,-12} {1}/{2} = {3:P0}" -f $e,$hit,$tot,$(if($tot){$hit/$tot}else{0})) }
Write-Output ''
Write-Output '--- MISDIRECTION AUDIT (every miss by direction) ---'
$costly=@{ 'real-break -> flake'='WASTES A RETRY'; 'infra -> real-break'='SKIPS A PAGE'; 'infra -> flake'='SKIPS A PAGE + wastes retry' }
$misses | Group-Object dir | Sort-Object Count -Descending | ForEach-Object {
  $tag = if($costly.ContainsKey($_.Name)){ "  <<< $($costly[$_.Name])" } else { '' }
  Write-Output ("[{0}] {1}{2}" -f $_.Count,$_.Name,$tag)
  foreach($m in $_.Group){ Write-Output ("      conf={0}  {1}  | ev: {2}" -f $m.conf, $m.meta, (norm $m.evidence).Substring(0,[Math]::Min(70,(norm $m.evidence).Length))) }
}
if(-not $misses){ Write-Output '(no misses)' }
