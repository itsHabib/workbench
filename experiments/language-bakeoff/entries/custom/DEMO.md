# The 60-second demo

```bash
./demo.sh          # about 5 seconds; needs Go 1.25+, sh, tr, awk, sort
```

The script builds `wb` and creates a fresh temporary directory. It runs the
six cases against [examples/pipeline.wb](examples/pipeline.wb), checks every
exit code, and then deletes the directory (`KEEP=1 ./demo.sh` keeps it).
The full output is [examples/demo.transcript.txt](examples/demo.transcript.txt),
and `go test` compares it with real output.

The source declares one kept input file and two pieces of work that read it:

```
keep file notes    { path "notes.txt"; text "the quick brown fox jumps over the lazy dog\n" }
run  command shout { stdin notes.path; argv "tr" "a-z" "A-Z"; stdout "notes.upper.txt" }
run  command count { stdin notes.path; argv "awk" "{ words += NF } END { print words }"; stdout "notes.count.txt" }
```

(The example file writes one attribute per line; `;` separates attributes
on a single line.)

## 1. Initial plan and apply

`wb intent` prints the normalized form: references resolved, dependency
order and owned paths. The plan then states what will happen and why,
before anything happens:

```
  + keep file    notes  create notes.txt (44 bytes)
  > run  command shout  run, writes notes.upper.txt
                        * it never completed
```

## 2. Unchanged re-apply

The apply reports `applied: 1 unchanged, 2 skipped`. The tests also check
that no file's bytes or modification time changed, that the journal gained
no records, and that no child process ran.

## 3. Input change

The plan shows the line diff and why the downstream work reruns:

```
  ~ keep file    notes  update notes.txt (44 -> 42 bytes)
                        | -the quick brown fox jumps over the lazy dog
                        | +the quick red fox jumps over the lazy dog
  > run  command shout  run, writes notes.upper.txt
                        * input notes changed (1153a4080f1f -> 1154f8e2b17b)
```

## 4. External edit between plan and apply

The saved plan is refused, nothing changes, and the re-plan names the edit:

```
$ wb apply -dir ws -plan plan.json
wb: refused: the plan is stale, so nothing was changed:
  notes: notes.txt was 1154f8e2b17b at plan time, now df97460881f2
(exit 3)
...
                        * notes.txt changed outside wb after #6; applying overwrites that edit
```

## 5. Partial failure, then retry

A shimmed `awk` on `PATH` makes `count` fail. `shout` completes, and the
retry does not repeat it:

```
  count  failed   awk: exit status 1: awk: injected failure  (journal #19-#20)
...
  = run  command shout  current, keeps notes.upper.txt
  > run  command count  run, writes notes.count.txt
                        * attempt #19 failed: awk: exit status 1: awk: injected failure
```

## 6. A third adapter without parser or planner changes

`keep dir` is a disposable scratch directory. Adding it took one adapter
file and one registry entry:

```go
reg, err := kind.NewRegistry(
	[]kind.Resource{file.Adapter{}, dir.Adapter{}}, // dir.Adapter{} is the new entry
	[]kind.Work{command.Adapter{}},
)
```

The demo appends two declarations from
[examples/scratch.wb](examples/scratch.wb) to the source:

```
  + keep dir     scratch  create directory scratch/
  > run  command words    run, writes words.txt
```

Delete `scratch/` and wb recreates it without rerunning `words`
(`= run  command words    current, keeps words.txt`).

<details>
<summary>The complete extension code: adapters/dir/dir.go</summary>

```go
type Adapter struct{}

func (Adapter) Schema() kind.Schema {
	return kind.Schema{
		Kind:    "dir",
		Fields:  []kind.Field{{Name: "path", Type: kind.OwnedPath, Required: true}},
		Exports: []string{"path"},
	}
}

func (Adapter) Observe(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	return kind.ObservePath(ws, d.One("path"))
}

func (Adapter) Propose(_ *foundation.Workspace, d intent.Decl, now kind.Fact) (kind.Proposal, error) {
	path := d.One("path")
	p := kind.Proposal{Version: foundation.Dir}
	switch now.Digest {
	case foundation.Dir:
		p.Action, p.Summary = kind.OK, path+"/ exists (contents unmanaged)"
	case foundation.Absent:
		p.Action, p.Summary = kind.Create, "create directory "+path+"/"
	default:
		p.Action, p.Summary = kind.Conflict, fmt.Sprintf("%s exists and is not a directory; wb will not replace it", path)
	}
	return p, nil
}

func (Adapter) Apply(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	path := d.One("path")
	if err := ws.Root().MkdirAll(path, 0o755); err != nil {
		return kind.Fact{}, err
	}
	return kind.ObservePath(ws, path)
}
```

</details>

## What to try next

- Break the source: `wb check examples/invalid.wb` reports seven positioned
  errors at once.
- Kill an apply mid-run. `TestInterruptedApplyIsVisibleToAFreshCaller` does
  this with SIGKILL, and the next `wb plan` reports the interrupted attempt.
- Compare with [baseline/pipeline.sh](baseline/pipeline.sh), the 28-line
  plain-script version. [README.md](README.md#compared-with-a-plain-script)
  weighs one against the other.
