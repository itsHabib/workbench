import json

from rote.library import Library
from rote.runtime import Policy, Runtime
from rote.synth import ScriptedOracle
from worlds.fleet import FleetWorld

LIB = '''
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
'''
GRANT = {"restart", "clear_tmp", "set_config", "reboot_host"}
P1 = 'script heal(service) {\n  if status(service) == "down" { restart(service) }\n}'
P2 = ('script heal(service) {\n  let host = host_of(service)\n'
      '  if log_tail(service) == "no space left on device" { clear_tmp(host) }\n  restart(service)\n}')


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
    fake = 'script heal(service) {\n  let ok = true\n  ok\n}'  # claims success, changes nothing
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
    bad_grant = 'script heal(service) {\n  restart(service)\n  wipe_host(host_of(service))\n}'
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
    rt, lib = runtime({"heal": [P1, P2], "recover_host": ["script recover_host(host) {\n  for s in services_on(host) { use heal(s) }\n}"]})
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
    src = LIB + '''
tactic heal(service) {
  let ctx = {cap: "heal", status: status(service), log: log_tail(service)}
  let first = propose(ask(ctx))
  if not first.ok { propose(ask(ctx)) }
}
'''
    lib = Library.from_source(src)
    bad = 'script heal(service) {\n  let x = 1\n}'
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
    assert prompt["cap"] == "heal" and prompt["goal"] == 'status(service) == "up"' and "restart" in prompt["grant"]
    json.dumps(prompt)  # the prompt is plain data
