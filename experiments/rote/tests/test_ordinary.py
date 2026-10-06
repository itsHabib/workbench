"""The baseline and the proxy leak behave as RESULTS.md says they do."""

from ordinary import agent_py, proxy_leak


def test_python_baseline_pays_for_what_it_cannot_see():
    rows = {r.tag: r for r in agent_py.run(verbose=False)}
    assert rows["E4"].disruptions >= 1, "the unvalidated else-branch rebooted a shared host"
    assert rows["E4"].outcome == "replayed" and rows["E4"].collateral == ["web"], (
        "and moved the outage to web"
    )
    assert any("P3a" in n for r in rows.values() for n in r.notes), (
        "the skill with an LLM call inside was retained"
    )
    assert sum(r.runtime_llm_calls for r in rows.values()) >= 1, "and it costs an LLM call at run time"
    assert rows["E9"].outcome == "parked" and rows["E9"].disruptions > rows["E8"].disruptions


def test_dynamic_grant_check_refuses_only_after_the_first_action_ran():
    world = agent_py.build_world()
    world.crash("web")
    g = agent_py.Guarded(world, agent_py.GRANT)
    try:
        agent_py.p3b_run(g, "web")
    except PermissionError as err:
        assert "wipe_host" in str(err)
    assert world.history == [("restart", ["web"])]


def test_proxy_tracer_misses_the_len_decision_and_rote_does_not():
    out = proxy_leak.main()
    assert out["python_guards"] == []
    assert out["python_disruptions_on_replay"] == 2
    assert any("len(services_on(host_of(service))) > 1" in g and "false" in g for g in out["rote_guards"])
