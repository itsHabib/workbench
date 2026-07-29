# Custody talk demo

This is the sanitized, Windows-native mechanism demo that supports the captured
real-Jira evidence. It uses a fake credential and a public HTTPS echo reflector
as a microscope: the audience can see the header custody injected without a
real vendor credential or employer response appearing on screen.

## What it proves

One run shows four boundaries:

1. no grant, unknown keys, and `TRACE` fail before forwarding;
2. the caller sends no vendor credential, but the upstream receives the
   configured fake Bearer credential;
3. a caller-supplied `Authorization` value is discarded and replaced;
4. the read grant admits Jira REST v2 reads while denying writes, v3, and
   cross-surface paths; the log then rolls up every verdict.

The reflector is presentation support, not a trust dependency. Store only this
obviously fake value:

```text
FAKE-CUSTODY-TALK-CREDENTIAL-NOT-A-SECRET
```

## Prepare without crossing the operator boundary

From a clean, reviewed Workbench checkout:

```powershell
go install ./cmd/custody
$demo = 'C:\path\to\workbench\docs\talks\custody-demo'
$env:CUSTODY_STATE = 'C:\Users\<you>\custody-talk\state'
$env:CUSTODY_KEY_DIR = 'C:\Users\<you>\custody-talk\key'
New-Item -ItemType Directory -Force $env:CUSTODY_STATE, $env:CUSTODY_KEY_DIR
Copy-Item "$demo\manifest.json" "$env:CUSTODY_STATE\manifest.json"
```

The state and mint-key directories must be siblings. Do not put the mint key
inside the state tree.

## Operator-only setup

The operator performs these two authority-bearing steps. The agent does not
store the fake credential or mint the grant:

```powershell
'FAKE-CUSTODY-TALK-CREDENTIAL-NOT-A-SECRET' |
  custody keys set -name custody-talk-fake

$env:CUSTODY_DEMO_GRANT = custody grant `
  -state $env:CUSTODY_STATE `
  -mint-key-dir $env:CUSTODY_KEY_DIR `
  -key jira-microscope `
  -actions read `
  -ttl 8h `
  -init
```

Use `-init` only the first time that mint-key directory is created.

## Run

Terminal 1:

```powershell
custody serve `
  -addr 127.0.0.1:8127 `
  -state $env:CUSTODY_STATE `
  -mint-key-dir $env:CUSTODY_KEY_DIR
```

Terminal 2:

```powershell
.\run-demo.ps1
```

Without `CUSTODY_DEMO_GRANT`, the script still runs the pre-grant floor and
stops with exit 2 at the operator boundary. With the grant, it runs the full
injection, isolation, scope, and audit sequence.

## Production evidence and claim boundary

After the mechanism is visible, show `production-evidence.md`: a previously
captured real Jira REST v2 read returned `200` through custody while the caller held no PAT.
Do not perform the corporate call live unless the operator explicitly chooses
to expose that dependency and has sanitized the screen.

Do not claim that the reflector proves Jira compatibility by itself. It proves
the broker mechanism; the captured real-Jira run proves production compatibility.
