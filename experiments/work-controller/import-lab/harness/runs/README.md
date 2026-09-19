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
