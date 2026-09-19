# Baseline receipts

Candidate heads:

- controller: `560f6338ad1d22a854ebc5d7275295a40dbe1b2b`
- native: `b6bacffa1c88dea1f98a13d48b7b70b0da1c1e60`

Frozen v1 oracle:

- protocol commit `063e003`, SHA-256
  `eb5bd5770f4a0c482cc061fb0726b7abdfbbe35da97fe647d527ae156447480a`
- harness commit `58ced50`, SHA-256
  `1b66e565757d83ad26759493ec0455575eaaf02ff24b8f3c02b4bde39c9f899e`

V2 corrected two unsupported oracle choices after the first build baseline, and
before the v2 build rerun. The correction is commit `9413919`; protocol SHA-256
is `7709b14c8e5128f713374f5de720acdb8c8f04810e4e0a714e6c04a3c78e6e3d`
and harness SHA-256 is
`7ac0568c55d90a8bfe3b81f5de3c62cff2092d8a750453f3b021e436f27f98aa`.
The v1 build results remain unchanged as false-positive evidence. The pressure
oracle did not change, so its original receipts remain the baseline.

| Candidate | V1 build | V2 build | Pressure |
| --- | --- | --- | --- |
| controller | 4 pass, 1 false-positive fail | 5 pass | 3 pass, 2 fail, 1 error |
| native | 2 pass, 2 false-positive fails, 1 dependent fail | 5 pass | 3 pass, 3 fail |

The static `browser_surface` check verifies controls and external asset URLs in
returned HTML. It is not a functional browser/JavaScript check. Independent
Chromium evidence belongs in the integration coordinator's receipts.

## Pressure fixture reproduction

`make_csv(50_000)` deterministically emits the header `id,name,email`, then
records `row-N,Name N,row-N@example.com` for N from 0 through 49,999. The
compact JSON request is 2,166,695 bytes with SHA-256
`afb44ea58603e45b7af2bed9ead331131264bc656edc450e0e50cd3e2206d61a`.
Both candidates returned exactly 50,000 records with the first and last
sentinels and no errors.

- controller large result: 3,816,742 response bytes, SHA-256
  `36fce0c80847310e9ac60f0683c064d77289beb557a7c6f4a0338efacf86d18d`
- native large result: 5,983,438 response bytes, SHA-256
  `b71ae14f09e8a0271997057dfb61fb4e27d293b7130119c0611446a46388b2af`
- over-limit request: 10,486,059 bytes, SHA-256
  `1bd6eae3999f1101080cc1bf67a6169ccb7db4fbbe4ba6a30261722e7fc118a1`

Each `exchanges.jsonl` retains every request/response byte count, SHA-256,
status, latency, and transport error. Small raw bodies and server error logs are
committed beside it. The 54 MiB of full large bodies and temporary data stores
was moved intact to `/tmp/import-lab-raw` on the runner host and intentionally
excluded from Git.

## V3 adversarial correction and baseline

V3 was frozen at commit `2dcefd2e12130fe3f084d92c4583f9c8b5360fb0`
before its baseline runs. Protocol SHA-256 is
`68e41f9a0c77a1435c03493c66d7e82d08fb0bff1dfdd9ba12b1bbb8044a1d36`
and harness SHA-256 is
`c162c182bd1e790c30eb1c15491bf85f334046e855415f8a450925ed3da960ce`.
The pinned candidate commits were exported with `git archive` to
`/tmp/import-v3-baseline.0Btvld`; live builder worktrees were not used.

V3 pressure independently checks the large concurrent request's accepted HTTP
status and exact 50,000 unique-record terminal result. It measures the small
request from submit through terminal polling against one five-second budget,
and describes the observed overlap only as client-request-thread overlap.

| Candidate | V3 pressure | Small end-to-end | V3 mid-import recovery | V3 accepted-result crash/retry |
| --- | --- | --- | --- | --- |
| controller | 3 pass, 2 fail, 1 error | 275 ms | not covered: synchronous completion | fail: retry returned a different import ID |
| native | 3 pass, 3 fail | 1 ms | not covered: synchronous completion | fail: retry returned a different import ID |

Both tightened concurrent large requests completed with 50,000 unique records,
both sentinels, and no errors. The original pressure conclusions did not change.
Recovery used a deterministic 8,499,121-byte request. Synchronous completion is
retained as `not_covered` for true mid-import recovery; the separate small
accepted-result crash/retry check records both baseline idempotency failures.

After the v3 runs, the full large bodies and data stores under
`/tmp/import-lab-raw` total 151 MiB. Compact results, exchange hashes/sizes,
small bodies, and process logs remain committed here.

## V4 observable malformed-CSV result and candidate runs

V4 was frozen at commit `e0117b8280022e713f5bb2653f633b522fa80c72`
before rerunning the exported baselines or final candidates. Protocol SHA-256 is
`45ced49012ffa7785e7326d1b0ac2c48663cab7a58e1858ff23eb42ca9e6b576`
and harness SHA-256 is
`de84f152f395d4682bedb448c3adce1037392ceebc81614b090ffd4157a19850`.
V4 judges malformed CSV by the shared observable contract: HTTP rejection, or
any terminal result with no records and useful row errors.

The frozen controller baseline's malformed CSV already satisfied that contract
with `completed`, zero records, and a useful error. Its earlier failure was an
oracle false positive. The corrected baseline pressure result is 4 pass, 1 fail
(request-ID idempotency), and 1 error (over-limit disconnect). Native remains 3
pass and 3 fail.

Controller intermediate commit `fb7ad711d7a15a8ff4d01218aaaf3c97ecb2e2d8`
was clean at execution and passed the harness:

- build: 5/5 pass
- pressure: 6/6 pass; the concurrency probe observed 1 ms health, 306 ms small
  import end to end, and an independently verified exact 50,000-record large
  result
- recovery: `accepted_result_crash_retry` pass;
  `mid_import_recovery` remains `not_covered` because the near-limit import
  completed synchronously before kill

The intermediate controller recovery evidence establishes durable accepted-result
idempotency across a process kill. It does not establish mid-task recovery.

This controller run is retained as intermediate evidence. A separate injected
storage-fault probe later found mutate-before-save behavior that this harness
does not exercise, so the experiment coordinator requested another controller
revision and final rerun.

Native final commit `e30c99780e109acd3a797f0016abcf1e0d82d644` was
clean at execution:

- build: 5/5 pass
- pressure: 6/6 pass; the concurrency probe observed 0 ms health, 50 ms small
  import end to end, and an independently verified exact 50,000-record large
  result
- recovery: `accepted_result_crash_retry` pass;
  `mid_import_recovery` remains `not_covered` because the near-limit import
  completed synchronously before kill

These are conformance counts and bounded latency observations, not a performance
comparison or winner decision.
