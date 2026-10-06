import json

from rote.library import Library
from rote.runtime import Policy, Runtime
from rote.synth import ScriptedOracle
from worlds.fleet import FleetWorld

LIB = """
world fleet {
  observe status(service): string
  observe host_of(service): string
  observe disk(host): int
  observe config(service): record
  observe log_tail(service): string
  observe ports_in_use(host): list
  observe services_on(host): list
  act restart(service): bool
  act clear_tmp(host): int
  act set_config(service, key, value): bool
  act reboot_host(host): int
  act wipe_host(host): bool
}
cap heal(service) { goal: status(service) == "up" }
cap recover_host(host) { goal: all(map(services_on(host), fn(s) { status(s) == "up" })) }
"""
GRANT = {"restart", "clear_tmp", "set_config", "reboot_host"}
P1 = 'script heal(service) {\n  if status(service) == "down" { restart(service) }\n}'
P2 = (
    "script heal(service) {\n  let host = host_of(service)\n"
    '  if log_tail(service) == "no space left on device" { clear_tmp(host) }\n  restart(service)\n}'
)


def world():
    w = FleetWorld("t")
    w.add_host("h1")
    w.add_service("web", "h1", 80)
    w.add_service("api", "h1", 81)
    return w


def runtime(cassette, mode="auto"):
    lib = Library.from_source(LIB)
    return Runtime(lib, Policy(grant=GRANT, mode=mode), ScriptedOracle(cassette)), lib


def test_synthesize_then_replay_without_the_oracle():
    rt, lib = runtime({"heal": [P1]})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "synthesized" and out.oracle_calls == 1 and w.obs_status("web") == "up"
    w.crash("api")
    out = rt.achieve("heal", ["api"], w)
    assert out.kind == "replayed" and out.oracle_calls == 0 and w.obs_status("api") == "up"
    assert [e.verdict for e in lib.caps["heal"].witnesses[0].evidence] == ["pass", "pass"]


def test_already_satisfied_does_nothing():
    rt, _ = runtime({"heal": [P1]})
    w = world()
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "already_satisfied" and w.history == [] and out.oracle_calls == 0


def test_only_the_goal_can_pass_a_proposal():
    fake = "script heal(service) {\n  let ok = true\n  ok\n}"  # claims success, changes nothing
    rt, lib = runtime({"heal": [fake, P1]})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "synthesized" and out.oracle_calls == 2
    assert [e.kind for e in out.events].count("checker_fail") == 1
    assert len(lib.caps["heal"].witnesses) == 1


def test_replay_only_policy_parks_instead_of_synthesizing():
    rt, lib = runtime({"heal": [P1]})
    w = world()
    w.crash("web")
    rt.achieve("heal", ["web"], w)
    locked = Runtime(lib, Policy(grant=GRANT, mode="replay-only"), None)
    w.disk_full("api")
    out = locked.achieve("heal", ["api"], w)
    assert out.kind == "parked" and out.oracle_calls == 0


def test_static_refusals_run_nothing_and_count_as_attempts():
    bad_ask = 'script heal(service) {\n  restart(service)\n  ask("x")\n}'
    bad_grant = "script heal(service) {\n  restart(service)\n  wipe_host(host_of(service))\n}"
    rt, _ = runtime({"heal": [bad_ask, bad_grant, P1]})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "synthesized" and out.oracle_calls == 3
    assert sum(1 for e in out.events if e.kind == "refused") == 2
    assert w.history == [("restart", ["web"])]  # only the good proposal ever ran


def test_oracle_exhaustion_is_a_named_failure():
    rt, _ = runtime({"heal": []})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "failed" and "no scripted proposal" in out.reason


def test_library_roundtrips_and_loaded_witnesses_replay(tmp_path):
    rt, lib = runtime({"heal": [P1, P2]})
    w = world()
    w.crash("web")
    rt.achieve("heal", ["web"], w)
    w.disk_full("api")
    rt.achieve("heal", ["api"], w)
    lib.save(tmp_path / "lib.json")
    loaded = Library.load(tmp_path / "lib.json")
    assert [x.hash for x in loaded.caps["heal"].witnesses] == [x.hash for x in lib.caps["heal"].witnesses]
    rt2 = Runtime(loaded, Policy(grant=GRANT, mode="replay-only"), None)
    w.disk_full("web")
    out = rt2.achieve("heal", ["web"], w)
    assert out.kind == "replayed" and w.obs_status("web") == "up"


def test_composition_repairs_locally():
    rt, lib = runtime(
        {
            "heal": [P1, P2],
            "recover_host": ["script recover_host(host) {\n  for s in services_on(host) { use heal(s) }\n}"],
        }
    )
    w = world()
    w.crash("web")
    rt.achieve("heal", ["web"], w)  # heal has P1 retained
    w.crash("api")
    w.disk_full("web")
    out = rt.achieve("recover_host", ["h1"], w)
    assert out.kind == "synthesized" and out.oracle_calls == 1
    assert len(lib.caps["heal"].witnesses) == 2  # the inner capability synthesized P2 for itself
    assert all(w.obs_status(s) == "up" for s in ("web", "api"))
    w.crash("api")
    out = rt.achieve("recover_host", ["h1"], w)
    assert out.kind == "replayed" and out.oracle_calls == 0


def test_in_language_tactic_drives_proposals():
    src = (
        LIB
        + """
tactic heal(service) {
  let ctx = {cap: "heal", status: status(service), log: log_tail(service)}
  let first = propose(ask(ctx))
  if not first.ok { propose(ask(ctx)) }
}
"""
    )
    lib = Library.from_source(src)
    bad = "script heal(service) {\n  let x = 1\n}"
    oracle = ScriptedOracle({"heal": [bad, P1]})
    rt = Runtime(lib, Policy(grant=GRANT), oracle)
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "synthesized" and oracle.calls == 2 and w.obs_status("web") == "up"


def test_scripted_oracle_logs_what_it_was_asked():
    rt, _ = runtime({"heal": [P1]})
    w = world()
    w.crash("web")
    rt.achieve("heal", ["web"], w)
    prompt = rt.oracle.log[0]["prompt"]
    assert (
        prompt["cap"] == "heal"
        and prompt["goal"] == 'status(service) == "up"'
        and "restart" in prompt["grant"]
    )
    json.dumps(prompt)  # the prompt is plain data


def test_when_clause_is_traced_into_guards_and_may_only_observe():
    guarded = (
        'script heal(service) when status(service) == "down" and log_tail(service) != "x" {\n'
        "  restart(service)\n}"
    )
    rt, lib = runtime({"heal": [guarded]})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "synthesized"
    guards = [g["expect"] for g in out.witness.guards]
    assert guards == [True, False]  # `status == "down"` held; `not (... != "x")` was false
    assert out.witness.prefix_len == 4  # status, guard, log_tail, guard; then restart
    bad = "script heal(service) when restart(service) { restart(service) }"
    rt, _ = runtime({"heal": [bad]})
    w = world()
    w.crash("web")
    out = rt.achieve("heal", ["web"], w)
    assert out.kind == "failed" and any("when-clause" in e.text for e in out.events) and w.history == []


def test_probe_is_shown_to_the_oracle():
    rt, _ = runtime({"heal": [P1]})
    rt.policy.probe = lambda world, cap, args: {"status": world.obs_status(args[0])}
    w = world()
    w.crash("web")
    rt.achieve("heal", ["web"], w)
    assert rt.oracle.log[0]["prompt"]["situation"] == {"status": "down"}


def test_revalidating_known_sources_takes_a_new_path_without_the_oracle():
    two_paths = (
        "script heal(service) {\n  let host = host_of(service)\n"
        '  if log_tail(service) == "no space left on device" { clear_tmp(host) }\n  restart(service)\n}'
    )
    rt, lib = runtime({"heal": [two_paths]})
    rt.policy.revalidate_sources = True
    w = world()
    w.disk_full("web")
    assert rt.achieve("heal", ["web"], w).kind == "synthesized"  # path: clear_tmp, restart
    w.crash("api")
    out = rt.achieve("heal", ["api"], w)  # guard on the log line fails; same source, other path
    assert out.kind == "revalidated" and out.oracle_calls == 0 and w.obs_status("api") == "up"
    assert len(lib.caps["heal"].witnesses) == 2 and {x.source for x in lib.caps["heal"].witnesses} == {
        two_paths
    }
    locked = Runtime(lib, Policy(grant=GRANT, mode="replay-only", revalidate_sources=True), None)
    w.lose_port("web")
    assert locked.achieve("heal", ["web"], w).kind == "parked"  # replay-only still runs nothing unvalidated


def test_dispatch_checks_every_prefix_first_and_continues_past_a_failed_replay():
    lock_fix = (
        "script heal(service) {\n  if lock_held(service) { remove_lock(service) }\n  restart(service)\n}"
    )
    lib_src = LIB.replace(
        "  act wipe_host(host): bool\n",
        "  act wipe_host(host): bool\n  observe lock_held(service): bool\n  act remove_lock(service): bool\n",
    )
    from live.world6 import FleetWorld6

    lib = Library.from_source(lib_src)
    oracle = ScriptedOracle({"heal": [P1, lock_fix]})
    rt = Runtime(lib, Policy(grant=GRANT | {"remove_lock"}), oracle)
    w = FleetWorld6("t")
    w.add_host("h1")
    w.add_service("web", "h1", 80)
    w.add_service("api", "h1", 81)
    w.crash("web")
    assert rt.achieve("heal", ["web"], w).kind == "synthesized"  # P1: status == down -> restart
    w.stale_lock("api")
    out = rt.achieve("heal", ["api"], w)  # P1 applies and fails; the oracle supplies the lock fix
    assert out.kind == "synthesized" and out.oracle_calls == 1
    w.stale_lock("web")
    out = rt.achieve("heal", ["web"], w)
    # both witnesses are applicable; the lock fix has the better evidence and runs first? P1 has 1 pass
    # 1 fail, the lock fix 1 pass 0 fails -> lock fix first, no wasted restart, no oracle call.
    assert out.kind == "replayed" and out.oracle_calls == 0 and out.witness.source == lock_fix
    assert [n for n, _ in w.history[-2:]] == ["remove_lock", "restart"]
