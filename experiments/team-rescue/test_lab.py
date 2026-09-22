"""Mechanism tests: scripted responses are never model-quality evidence."""
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import lab


def action(kind="assess", **fields):
    return dict(action=kind, worker="", message="evidence", memory="continue here", files=[], **fields)


def receipt(response):
    return {"response": response, "usage": {"input_tokens": 50, "output_tokens": 10},
            "error": None, "elapsed_seconds": 0.01}


def checked(run, state):
    state["checks"] = {"passed": "fixed" in (run / "versions" / state["version"] / "service.py").read_text(),
                       "checks": [], "version": state["version"]}
    return state["checks"]


class ActionTests(unittest.TestCase):
    def test_role_and_file_boundaries(self):
        invalid = action("edit")
        invalid["files"] = [{"path": "../../verifier.py", "content": "pass"}]
        with self.assertRaises(ValueError):
            lab.validate_action(invalid, "worker-1", "lead")
        invalid["files"][0]["path"] = "service.py"
        with self.assertRaises(ValueError):
            lab.validate_action(invalid, "lead", "lead")
        with self.assertRaises(ValueError):
            lab.validate_action(action("finish"), "challenger", "pair")
        with self.assertRaises(ValueError):
            lab.validate_action(action("challenge"), "lead", "lead")


@unittest.skipUnless(os.environ.get("FLEET_BIN"), "set FLEET_BIN to the reviewed Fleet job binary")
class FleetIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.run = Path(self.temporary.name) / "trial"
        source = Path(self.temporary.name) / "source"
        source.mkdir()
        (source / "service.py").write_text("# unfinished\n")
        self.config = {"fleet": os.environ["FLEET_BIN"], "mode": "lead", "model": "test",
                       "lead_model": "test", "critic_model": "test", "max_calls": 6,
                       "call_timeout": 5, "wall_seconds": 15}
        lab.initialize(self.run, self.config, source)

    def step(self, response):
        return lab.run_steps(self.run, 1, complete_fn=lambda *a, **kw: receipt(response), check_fn=checked)

    def dispatch(self):
        value = action("dispatch")
        value["worker"] = "worker-1"
        value["message"] = "repair the implementation"
        return value

    def edit(self):
        value = action("edit")
        value["files"] = [{"path": "service.py", "content": "# fixed\n"}]
        return value

    def test_dispatch_edit_accept_and_memory_survive_restart(self):
        self.step(self.dispatch())
        self.assertEqual(lab.load(self.run)["next"], "worker-1")
        self.step(self.edit())
        state = self.step(action("finish"))
        self.assertEqual(state["status"], "complete")
        self.assertEqual(len(state["calls"]), 3)
        self.assertEqual(state["tokens_known"], 180)
        self.assertEqual(state["memories"]["worker-1"], "continue here")
        metrics = lab.fleet(self.run, self.config, "metrics")
        self.assertEqual(metrics["states"]["accepted"], 3)

    def test_declaring_done_cannot_bypass_checker(self):
        state = self.step(action("finish"))
        self.assertEqual(state["status"], "ready")
        self.assertFalse(state["checks"]["passed"])

    def test_uncertain_call_is_reserved_not_replayed(self):
        def crash(*args, **kwargs):
            raise RuntimeError("simulated driver crash")
        with self.assertRaisesRegex(RuntimeError, "crash"):
            lab.run_steps(self.run, 1, complete_fn=crash, check_fn=checked)
        self.assertEqual(lab.summary(self.run)["reserved_calls"], 1)
        with self.assertRaisesRegex(RuntimeError, "uncertain"):
            self.step(action())
        state = lab.mutate(self.run, "abandon", "inspect and continue")
        self.assertEqual(len(state["calls"]), 1)
        self.assertEqual(state["unknown_usage_calls"], 1)
        self.assertEqual(state["active_seconds"], 5)
        state = self.step(action())
        self.assertEqual(len(state["calls"]), 2)

    def test_crash_after_fleet_accept_reconciles_without_model_retry(self):
        original = lab.save
        def crash_after_accept(run, state):
            if state["pending"] is None and len(state["calls"]) == 1:
                raise RuntimeError("crash before mission snapshot")
            return original(run, state)
        with patch.object(lab, "save", crash_after_accept):
            with self.assertRaisesRegex(RuntimeError, "snapshot"):
                self.step(self.dispatch())
        # Stored model result is replayed; only the next worker receives a fresh call.
        state = self.step(self.edit())
        self.assertEqual(len(state["calls"]), 2)
        self.assertEqual(len(state["history"]), 2)
        self.assertEqual(lab.fleet(self.run, self.config, "metrics")["attempts"], 2)

    def test_stop_and_call_budget_prevent_transport(self):
        (self.run / "STOP").touch()
        with patch.object(lab, "supervised_complete", side_effect=AssertionError("must not call")):
            lab.run_steps(self.run, check_fn=checked)
        self.assertEqual(lab.load(self.run)["status"], "stopped")
        (self.run / "STOP").unlink()
        state = lab.load(self.run)
        state["status"] = "ready"
        state["calls"] = [{}] * 6
        lab.save(self.run, state)
        with patch.object(lab, "supervised_complete", side_effect=AssertionError("must not call")):
            lab.run_steps(self.run, check_fn=checked)
        self.assertEqual(lab.load(self.run)["status"], "budget")

    def test_human_question_and_requirement_change(self):
        state = self.step(action("ask"))
        self.assertEqual(state["status"], "needs_input")
        lab.mutate(self.run, "answer", "Local implementation is already authorized")
        self.assertEqual(lab.load(self.run)["human_interventions"], 1)
        state = lab.mutate(self.run, "change", "Add replay")
        self.assertEqual(state["phase"], 2)
        self.assertEqual(state["changes"], ["Add replay"])

    def test_worker_blocker_goes_to_lead_before_human(self):
        self.step(self.dispatch())
        state = self.step(action("ask"))
        self.assertEqual(state["status"], "ready")
        self.assertEqual(state["next"], "lead")
        self.assertEqual(state["human_interventions"], 0)
        self.assertIn("blocker", state["task"])

    def test_noop_edits_consume_call_without_creating_version(self):
        self.step(self.dispatch())
        edit = self.edit()
        edit["files"][0]["content"] = "# unfinished\n"
        state = self.step(edit)
        self.assertEqual(state["version"], "0000")
        self.assertEqual(len(state["calls"]), 2)
        self.assertIn("no source changed", state["calls"][-1]["error"])
        self.assertFalse((self.run / "versions" / "0002").exists())

    def test_last_allowed_call_immediately_reports_budget(self):
        state = lab.load(self.run)
        state["calls"] = [{}] * 5
        lab.save(self.run, state)
        state = self.step(action())
        self.assertEqual(len(state["calls"]), 6)
        self.assertEqual(state["status"], "budget")

    def test_malformed_response_does_not_touch_files(self):
        self.step(self.dispatch())
        edit = self.edit()
        edit["files"][0]["path"] = "../mission.json"
        state = self.step(edit)
        self.assertEqual(state["version"], "0000")
        self.assertIn("path", state["calls"][-1]["error"])

    def test_retried_result_is_rejected_before_candidate_execution(self):
        self.step(self.dispatch())
        value = receipt(self.edit())
        def interrupt(*args, **kwargs):
            raise RuntimeError("interrupted")
        with self.assertRaises(RuntimeError):
            lab.run_steps(self.run, 1, complete_fn=interrupt, check_fn=checked)
        state = lab.load(self.run)
        pending = state["pending"]
        lab.fleet(self.run, self.config, "complete", id=pending["job"], worker=pending["worker"],
                  token=pending["token"], result=json.dumps(value["response"], sort_keys=True))
        lab.fleet(self.run, self.config, "retry", id=pending["job"], token=pending["token"], evidence="retracted")
        with self.assertRaisesRegex(RuntimeError, "no longer reported"):
            lab.finish_pending(self.run, state, self.config, value,
                               check_fn=lambda *args: self.fail("must not run candidate"))
        self.assertFalse((self.run / "versions" / "0002").exists())

    def test_config_drift_and_two_drivers_refuse(self):
        with lab.locked(self.run):
            with self.assertRaisesRegex(RuntimeError, "another driver"):
                self.step(action())
        config = json.loads((self.run / "config.json").read_text())
        config["max_calls"] = 999
        lab.atomic(self.run / "config.json", config)
        with self.assertRaisesRegex(RuntimeError, "config changed"):
            self.step(action())


if __name__ == "__main__":
    unittest.main()
