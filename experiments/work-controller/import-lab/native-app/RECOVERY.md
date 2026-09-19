# Native import desk recovery

The native app is self-contained in `server.py`, `test_server.py`, `TASKS.md`,
and this note. The shared `BRIEF.md` and `runs/local-01/PRESSURE.md` supplied
the round-two contract; no additional native-app context was missing.

Run from the repository root:

```sh
python3 -m unittest discover -s experiments/work-controller/import-lab/native-app -p 'test_*.py' -v
```

The app uses SQLite under `--data-dir`, accepts only local requests, and keeps
the synchronous import API. Browser validation needs the dedicated Chromium
executable and a free port; avoid the parent preview ports 4341 and 4342.
