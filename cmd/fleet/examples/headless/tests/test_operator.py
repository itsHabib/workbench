"""Offline operator regressions. No provider, watcher, network or installed config."""
import contextlib
import io
import json
from pathlib import Path
import shutil
import signal
import sys
import tempfile
import tomllib
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import audit
import lab


class OperatorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="headless-cli-")
        cls.binary = Path(cls.build.name) / "fleet"
        lab.run(["go", "build", "-o", cls.binary, "./cmd/fleet"], lab.REPO, timeout=900)

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="headless-operator-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        for name in ("state/watch/delivery", "org", "lanes", "projection-home", "bin"):
            (self.root / name).mkdir(parents=True)
        shutil.copy2(self.binary, self.root / "bin/fleet")
        lab.write_json(self.root / "lab.json", {"example": "headless-workbench-v1", "root": str(self.root)})

    def test_foreign_root_is_refused(self):
        self.assertEqual(lab.lab_root(self.root), self.root)
        lab.write_json(self.root / "lab.json", {"example": "headless-workbench-v1", "root": "/another-run"})
        with self.assertRaises(RuntimeError):
            lab.lab_root(self.root)

    def test_exit_requires_collected_exit_code(self):
        path = self.root / "state/watch/delivery/attempt.meta.json"
        path.write_text("{}")
        self.assertFalse(lab.exits_collected(self.root))
        path = path.with_name("attempt.exit.json")
        path.write_text('{}')
        self.assertFalse(lab.exits_collected(self.root))
        lab.write_json(path, {"what": "delivery-exited", "exit_code": 130})
        self.assertTrue(lab.exits_collected(self.root))

    def test_missing_or_mismatched_provider_state_is_unknown(self):
        attempt = str(self.root / "state/watch/delivery/attempt")
        lab.write_json(Path(attempt + ".meta.json"), {"attempt": attempt, "provider": "codex", "state_file": attempt + ".state.json"})
        with self.assertRaises(FileNotFoundError):
            lab.states(self.root)
        lab.write_json(Path(attempt + ".state.json"), {"attempt": "another", "provider": "codex", "provider_terminal": True})
        with self.assertRaises(RuntimeError):
            lab.states(self.root)

    def test_uncertain_watcher_does_not_project(self):
        lab.write_json(self.root / "resolved.json", {})
        result = type("Result", (), {"stdout": '{"watcher":"stale"}'})()
        with patch.object(lab, "fleet", return_value=result), self.assertRaises(RuntimeError):
            lab.card_projection(self.root, apply=True)

    def test_collection_error_still_stops_watcher_and_closes_log(self):
        (self.root / "control").mkdir()
        (self.root / "control/stop-requested").touch()
        (self.root / "card.md").write_text("fixture")
        (self.root / "RUN.md").write_text("fixture")
        lab.write_json(self.root / "state/deliver.json", {})
        lab.write_json(self.root / "resolved.json", {kind: {"card": str(self.root / "card.md")} for kind in lab.ROLES})
        watcher = Mock(pid=123, poll=Mock(return_value=None))
        with patch.object(lab.subprocess, "Popen", return_value=watcher) as start, patch.object(lab, "stop", side_effect=RuntimeError("status unavailable")), contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "status unavailable"):
            lab.operate(self.root)
        watcher.send_signal.assert_called_once_with(signal.SIGINT)
        watcher.wait.assert_called_once_with(timeout=15)
        self.assertTrue(start.call_args.kwargs["stdout"].closed)
        self.assertEqual(json.loads((self.root / "control/collection-error.json").read_text())["error"], "status unavailable")

    def test_parent_session_does_not_reach_lab_agents(self):
        lab.write_json(self.root / "resolved.json", {"provider": {"name": "claude", "runtime_home": "/sdk"}})
        inherited = {"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "parent", "CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/s",
                     "CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CLAUDE_CONFIG_DIR": "/config", "CLAUDE_CODE_OAUTH_TOKEN": "token",
                     "CLAUDE_CODE_USE_VERTEX": "1", "PATH": "/usr/bin", "HOME": "/home/operator"}
        with patch.dict(lab.os.environ, inherited, clear=True):
            env = lab.env_for(self.root)
        self.assertFalse({"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_ENTRYPOINT"} & set(env))
        self.assertEqual((env["CLAUDE_CONFIG_DIR"], env["HOME"], env["FLEET_RUNTIME_HOME"]), ("/config", "/home/operator", "/sdk"))
        self.assertEqual((env["CLAUDE_CODE_OAUTH_TOKEN"], env["CLAUDE_CODE_USE_VERTEX"]), ("token", "1"))

    def test_claude_policy_keeps_fleet_hooks_and_denies(self):
        checkout = self.root / "seat"
        (checkout / ".claude").mkdir(parents=True)
        hooks = {"SessionStart": [{"hooks": [{"type": "command", "command": "fleet hook claude"}]}]}
        lane_deny = "Bash(git reset --hard:*)"
        lab.write_json(checkout / ".claude/settings.local.json", {"hooks": hooks, "permissions": {"allow": ["Bash(FLEET_ALLOW_SLOW=x:*)"], "deny": [lane_deny]}})
        lab.claude_settings(checkout, self.root)
        settings = json.loads((checkout / ".claude/settings.local.json").read_text())
        self.assertEqual(settings["hooks"], hooks)
        self.assertLessEqual({"Bash(FLEET_ALLOW_SLOW=x:*)", *lab.claude_allow(self.root)}, set(settings["permissions"]["allow"]))
        self.assertFalse({"Read", "Write", "Edit"} & set(settings["permissions"]["allow"]), "bare file-tool allows escape the lab")
        self.assertIn(f"Edit(/{self.root}/**)", settings["permissions"]["allow"])
        self.assertLessEqual({lane_deny, *lab.CLAUDE_DENY}, set(settings["permissions"]["deny"]))
        self.assertEqual(settings["permissions"]["additionalDirectories"], [str(self.root)])
        self.assertIs(settings["autoMemoryEnabled"], False)

    def test_claude_wrapper_overrides_sdk_settings_and_appends_the_live_card(self):
        runtime = self.root / "sdk"
        (runtime / "node_modules/@anthropic-ai/claude-agent-sdk").mkdir(parents=True)
        (runtime / "node_modules/@anthropic-ai/claude-agent-sdk/package.json").write_text("{}")
        record = self.root / "argv.json"
        fake = self.root / "real-claude"
        fake.write_text("#!/usr/bin/env python3\nimport json,os,sys\n"
                        f"open({str(record)!r}, 'w').write(json.dumps({{'argv': sys.argv[1:], 'memory': os.environ.get('CLAUDE_CODE_DISABLE_AUTO_MEMORY')}}))\n")
        fake.chmod(0o700)
        seats = [self.root / name for name in ("lead", "author", "verifier")]
        for seat in seats:
            (seat / ".claude").mkdir(parents=True)
            lab.write_json(seat / ".claude/settings.local.json", {"hooks": {}})
        with patch.object(lab.shutil, "which", return_value=str(fake)):
            info = lab.claude_wrapper(self.root, seats, runtime)
        self.assertEqual(info["cards"][str(seats[1])], str(self.root / "lanes/author/card.md"))
        lab.run([self.root / "bin/claude", "--setting-sources=user,project,local", "--print"], seats[1])
        observed = json.loads(record.read_text())
        argv = observed["argv"]
        # The CLI keeps the last --setting-sources, so the lab's must follow the SDK's.
        self.assertGreater(argv.index("project,local"), argv.index("--setting-sources=user,project,local"))
        self.assertEqual(argv[-2:], ["--append-system-prompt-file", str(self.root / "lanes/author/card.md")])
        self.assertEqual(observed["memory"], "1")

    def test_cancelled_supervisor_must_end_interrupted(self):
        lab.ended_otherwise([{"attempt": "a", "provider_terminal": True, "provider_state": "interrupted"}], "a")
        lab.ended_otherwise([{"attempt": "b", "provider_terminal": True, "provider_state": "failed"}], "a")
        with self.assertRaisesRegex(RuntimeError, "not interrupted"):
            lab.ended_otherwise([{"attempt": "a", "provider_terminal": True, "provider_state": "failed"}], "a")

    def test_rooms_evidence_requires_identical_patch_success_and_cleanup(self):
        patch_file = self.root / "worker.patch"
        patch_file.write_bytes(b"diff\n")
        digest = audit.digest(patch_file)
        out = self.root / "result/rooms"
        out.mkdir(parents=True)
        (self.root / "bin/rooms-check.py").write_text("adapter")
        info = {"rooms": {"out": str(out), "adapter_sha256": audit.digest(self.root / "bin/rooms-check.py")},
                "verifier": {"cwd": "/lab/verifier"}}
        lab.write_json(out / "summary.json", {"input_patch_sha256": digest, "returned_patch_sha256": digest,
                                              "cli_exit": 0, "command_status": "succeeded", "command_exit": 0})
        (out / "lifecycle.ndjson").write_text('{"event":"collection_done"}\n{"event":"cleanup_done"}\n')
        receipt = {"head": "h", "kind": "rooms", "verdict": "pass", "dirty": False, "cwd": "/lab/verifier", "at": 1}
        checks, _ = audit.rooms_evidence(self.root, info, patch_file, [receipt], "h")
        self.assertTrue(all(checks.values()), checks)
        lab.write_json(out / "summary.json", {"input_patch_sha256": digest, "returned_patch_sha256": "other",
                                              "cli_exit": 0, "command_status": "succeeded", "command_exit": 0})
        (out / "lifecycle.ndjson").write_text('{"event":"collection_done"}\n')
        checks, _ = audit.rooms_evidence(self.root, info, patch_file, [receipt], "h")
        self.assertEqual({k for k, v in checks.items() if not v}, {"rooms_identical_patch", "rooms_collected"})

    def test_claude_draft_continuity_uses_attempt_launch_times(self):
        draft = self.root / "seat/PLAN.md"
        body = "plan\n"
        delivery = self.root / "state/watch/delivery"

        def attempt(name, launched, *edits):
            lab.write_json(delivery / (name + ".meta.json"), {"at": launched})
            events = [{"type": "assistant", "session_id": "author", "message": {"content": [
                {"type": "tool_use", "name": tool, "input": {"file_path": str(draft), **fields}}]}} for tool, fields in edits]
            (delivery / (name + ".trace.jsonl")).write_text("".join(json.dumps(e) + "\n" for e in events))

        attempt("first", 100, ("Write", {"content": body}))
        interruption = {"draft_sha256": audit.hashlib.sha256(body.encode()).hexdigest(), "requested_at": 150, "resumed_at": 200}
        self.assertTrue(audit.draft_continuity(self.root, draft, interruption, "author"))
        self.assertFalse(audit.draft_continuity(self.root, draft, interruption, "replacement"))
        attempt("later", 300, ("Edit", {"old_string": "plan", "new_string": "done"}))
        self.assertTrue(audit.draft_continuity(self.root, draft, interruption, "author"))
        attempt("during", 180, ("Edit", {"old_string": "plan", "new_string": "raced"}))
        self.assertFalse(audit.draft_continuity(self.root, draft, interruption, "author"))

    def test_edit_once_inspect_then_project_using_real_fleet(self):
        info = {}
        for kind in lab.ROLES:
            checkout = self.root / ("workbench-lead" if kind == "supervisor" else "workbench-" + kind)
            lab.run(["git", "init", checkout], self.root)
            lane = self.root / "lanes" / kind
            lane.mkdir()
            card = lane / "card.md"
            card.write_text("Initial role purpose.\n")
            lab.write_json(lane / "manifest.json", {"kind": kind, "card": "card.md", "denies": [], "requires": [], "produces": None, "slots": 0})
            projection = {**lab.env_for(self.root), "CODEX_HOME": str(self.root / "projection-home")}
            lab.run([self.root / "bin/fleet", "role", checkout, kind + ":test", "--tenant", "headless-lab"], self.root, projection)
            info[kind] = {"cwd": str(checkout), "card": str(card), "address": kind + ":test"}
        lab.write_json(self.root / "resolved.json", info)
        target = Path(info["author"]["cwd"]) / ".codex/config.toml"
        before = target.read_bytes(), target.stat().st_mtime_ns
        Path(info["author"]["card"]).write_text("Changed purpose from the one source.\n")
        with contextlib.redirect_stdout(io.StringIO()) as output:
            lab.card_projection(self.root)
        self.assertIn('"projection_matches": false', output.getvalue())
        self.assertEqual((target.read_bytes(), target.stat().st_mtime_ns), before)
        with contextlib.redirect_stdout(io.StringIO()):
            lab.card_projection(self.root, apply=True)
        self.assertIn("Changed purpose", tomllib.loads(target.read_text())["developer_instructions"])
        projected = target.read_bytes(), target.stat().st_mtime_ns
        with contextlib.redirect_stdout(io.StringIO()):
            lab.card_projection(self.root, apply=True)
        self.assertEqual((target.read_bytes(), target.stat().st_mtime_ns), projected)


if __name__ == "__main__":
    unittest.main()
