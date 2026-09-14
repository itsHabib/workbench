# Passage

A light POC for selected work phases and checked handoffs. Start with the
[design and Rooms/Fleet reconciliation](../../docs/features/passage/spec.md).

Agents choose how to work within a phase. Passage records an attributed judgment
and the evidence it used, then refuses advancement when required evidence is
missing, failed or stale. The example has discovery, design, plan, implementation,
verification and release; the contract can select fewer phases before init.

## Try the whole example

```sh
python3 cmd/passage/examples/demo.py
```

Requires Go, Python 3 and Git. Builds a temporary binary and creates an isolated
subject repository and work record. It prints the evidence directory containing
`work.json`, `commands.json` and `summary.json`. It starts no agents, Rooms hosts,
watchers or model calls. It exercises an actual Go test failure and repair;
document judgments and release are fixtures, not independent review or a merge.

## Use the command

```sh
go build -o /tmp/passage ./cmd/passage
/tmp/passage init --work /tmp/change.json --root /path/to/checkout \
  --contract cmd/passage/examples/contract.json
/tmp/passage status --work /tmp/change.json
/tmp/passage record --work /tmp/change.json --expect 0 \
  --requirement sources-assessed --verdict pass --by michael \
  --note 'Sources answer the design questions; uncertainties are listed.' \
  --evidence /tmp/discovery-review.txt
/tmp/passage check --work /tmp/change.json
/tmp/passage advance --work /tmp/change.json --expect 1 --by michael \
  --note 'Discovery accepted as input to design.'
/tmp/passage history --work /tmp/change.json
```

Create the input documents named in the contract under the root. Each phase has
a name, brief, file `inputs`, optional `git` binding, and `requires` names. These
names specify judgments; Passage does not run tests or invent a quality threshold.
Input paths must stay within the root. File inputs and evidence must be regular
files; evidence is bounded UTF-8 text and is retained with its digest.

Keep state and logs outside the Git worktree. Git-bound phases require a clean
working tree, including untracked files. Document-only phases can precede a Git
repository. All mutations need `--by`, `--note`, and the current `--expect`
revision shown by status. Init freezes the contract in the work record.

`status`, `check` and `history` print JSON. `check` exits 1 if evidence is not
ready; `status` can successfully explain an unready record. Other refused actions
and runtime errors exit 1; argument parsing errors exit 2. Success exits 0.

## Changed inputs and interrupted actions

```sh
/tmp/passage reopen --work /tmp/change.json --expect CURRENT_REVISION \
  --phase design --by michael --note 'Requirement changed; reconsider the design.'
```

Reopen preserves history and clears active receipts/admissions from that phase
onward. It cannot skip forward. Current receipts use the latest recorded verdict
for each requirement; a later failure supersedes a pass. A changed input invalidates
the associated evidence, including evidence accepted at an earlier handoff.

After a lost response, inspect history. Reusing the old revision refuses instead
of repeating a mutation. Competing writers refuse at the lock or revision check.
An interrupted writer can leave `WORK.lock`; do not remove it until you have
established that the writer stopped. Preserve the record and temporary files for
inspection, then remove only that stale lock. There is no automatic stale-lock
takeover. The containing directory must exist.

## Deliberate limits

This is a local trusted-user specimen. Attribution is supplied by the caller,
not authenticated. A retained test log is evidence for a judgment, not proof the
test was run on that subject. The record is inspectable history, not a signed or
externally anchored audit chain. An arbitrary disk writer can rewrite it.

Inputs can change outside Passage's lock; consumers must recheck the actual
subject before effects. Evidence text is limited to 256 KiB and the record to
16 MiB; large Rooms bundles need a future explicit artifact-reference design.
Atomic replacement protects against partial record publication, not every
power-loss case or unsupported filesystem. Cross-host writers are out of scope.

`complete` means the selected record's handoffs were accepted. It never means a
merge or deployment was authorized or performed. Release is an observed external
outcome. There is no automatic Rooms adapter, Fleet dispatch, Gate invocation,
agent launcher, daemon or repository hook. Those remain visible design seams.
