"""Oracles for the live experiment, and the probe every arm shows them.

  HeuristicOracle  -- a deterministic STAND-IN for a model (the dry run). It diagnoses
                      the cause from the probe and returns a fixed script per cause,
                      first a generic "kitchen-sink" runbook, then the targeted fix.
                      Nothing it produces is model output.
  LoggedLiveOracle -- the real thing: one Claude call per proposal, with token usage
                      and a JSONL transcript. Needs ANTHROPIC_API_KEY.
"""

from __future__ import annotations

import json
import re
import threading
import time
from dataclasses import dataclass, field
from typing import Any

from rote.synth import GRAMMAR_CARD, OracleExhausted

__all__ = ["probe6", "HeuristicOracle", "LoggedLiveOracle", "CallBudget"]


def probe6(world: Any, cap: str, args: list[Any]) -> dict[str, Any]:
    """What a diligent operator would look at first. Observations only."""
    s = args[0]
    host = world.observe("host_of", [s])
    deps = world.observe("depends_on", [s])
    return {
        "service": s,
        "status": world.observe("status", [s]),
        "host": host,
        "disk": world.observe("disk", [host]),
        "config": world.observe("config", [s]),
        "log_tail": world.observe("log_tail", [s]),
        "ports_in_use": world.observe("ports_in_use", [host]),
        "lock_held": world.observe("lock_held", [s]),
        "depends_on": deps,
        "dependency_status": {d: world.observe("status", [d]) for d in deps},
    }


# ---- the heuristic stand-in ---------------------------------------------------------

GENERIC = '''script heal(service) {
  let host = host_of(service)
  if disk(host) > 90 { clear_tmp(host) }
  if lock_held(service) { remove_lock(service) }
  for d in depends_on(service) { use heal(d) }
  restart(service)
}'''

TARGETED = {
    "crash": '''script heal(service) {
  if status(service) == "down" { restart(service) }
}''',
    "disk": '''script heal(service) {
  let host = host_of(service)
  if disk(host) > 90 { clear_tmp(host) }
  restart(service)
}''',
    "lock": '''script heal(service) {
  if lock_held(service) { remove_lock(service) }
  restart(service)
}''',
    "dep": '''script heal(service) {
  for d in depends_on(service) { use heal(d) }
  restart(service)
}''',
    "lostport": '''script heal(service) {
  let host = host_of(service)
  if not has(config(service), "port") {
    let free = max_of(ports_in_use(host), 8000) + 1
    set_config(service, "port", free)
  }
  restart(service)
}''',
    "port": '''script heal(service) {
  let host = host_of(service)
  let cfg = config(service)
  if contains(ports_in_use(host), cfg.port) {
    set_config(service, "port", max_of(ports_in_use(host), cfg.port) + 1)
  }
  restart(service)
}''',
    "port_nested": '''script heal(service) {
  let host = host_of(service)
  let cfg = config(service)
  if contains(ports_in_use(host), cfg.listen.port) {
    set_config(service, "listen", {port: max_of(ports_in_use(host), cfg.listen.port) + 1})
  }
  restart(service)
}''',
}

WHEN = {  # the precondition a careful author would write for each targeted script
    "generic": 'status(service) == "down"',
    "crash": 'status(service) == "down"',
    "disk": 'disk(host_of(service)) > 90',
    "lock": 'lock_held(service)',
    "dep": 'any(map(depends_on(service), fn(d) { status(d) != "up" }))',
    "lostport": 'not has(config(service), "port") and not has(config(service), "listen")',
    "port": 'contains(ports_in_use(host_of(service)), config(service).port)',
    "port_nested": 'contains(ports_in_use(host_of(service)), config(service).listen.port)',
}


def diagnose(sit: dict[str, Any]) -> str:
    cfg = sit.get("config") or {}
    if sit.get("disk", 0) > 90:
        return "disk"
    if sit.get("lock_held"):
        return "lock"
    if any(v != "up" for v in (sit.get("dependency_status") or {}).values()):
        return "dep"
    port = cfg.get("port") if isinstance(cfg.get("port"), int) else (cfg.get("listen") or {}).get("port")
    if port is None:
        return "lostport"
    if port in (sit.get("ports_in_use") or []) or "address already in use" in sit.get("log_tail", ""):
        return "port_nested" if "listen" in cfg else "port"
    return "crash"


def with_when(src: str, cond: str) -> str:
    head, rest = src.split("{", 1)
    return f"{head.rstrip()} when {cond} {{{rest}"


@dataclass
class HeuristicOracle:
    """NOT A MODEL. Deterministic scripts chosen from the probe; generic first, targeted second."""

    calls: int = 0
    log: list[dict[str, Any]] = field(default_factory=list)

    def ask(self, prompt: dict[str, Any]) -> str:
        self.calls += 1
        sit = prompt.get("situation") or {}
        attempts = prompt.get("attempts") or []
        fmt = prompt.get("format", "guards")
        cause = diagnose(sit)
        key = "generic" if not attempts else cause
        src = GENERIC if key == "generic" else TARGETED[cause]
        if len(attempts) >= 2:  # the targeted fix failed too: nothing better to offer
            raise OracleExhausted("heuristic oracle has no further proposal")
        if fmt == "when":
            src = with_when(src, WHEN[key])
        self.log.append({"call": self.calls, "cause": cause, "key": key, "format": fmt})
        return src


# ---- the live oracle --------------------------------------------------------------------

class CallBudget:
    def __init__(self, max_calls: int):
        self.max_calls, self.used = max_calls, 0
        self.lock = threading.Lock()

    def take(self) -> None:
        with self.lock:
            if self.used >= self.max_calls:
                raise OracleExhausted(f"call budget of {self.max_calls} exhausted")
            self.used += 1


SYSTEM = (
    "You write Rote scripts for an operator's capability library. The user message is a JSON object: "
    "the capability (`cap`, `params`, `goal`), the world signature (`world`: name -> observe/act and arity), the caller's "
    "`grant` (the only actions you may call), a `situation` probe of fresh observations, `observed` values seen while "
    "dispatching, `dispatch` notes, and `attempts` (earlier proposals this episode and why each failed). Reply with exactly one "
    "```rote fenced block containing a single `script` declaration and nothing else outside it.\n\n" + GRAMMAR_CARD +
    "\nRules: observe before you act; call only functions in the world signature and only granted actions; never call ask(); "
    "a script may `use heal(x)` to repair another service first. "
    "If `format` is \"when\", add an applicability condition after the parameter list: "
    "`script heal(service) when <observe-only expression> { ... }`; it will be evaluated on future incidents to decide, "
    "without you, whether to reuse this script, so make it exactly as specific as the fix requires. "
    "If `library` is present, you may copy one of those scripts verbatim when it fits, or adapt one, or write a new one."
)


@dataclass
class LoggedLiveOracle:
    """One Claude call per proposal. Records usage and a transcript. Requires ANTHROPIC_API_KEY."""

    model: str = "claude-opus-5-5"
    effort: str = "medium"
    max_tokens: int = 16000
    budget: CallBudget | None = None
    transcript_path: str | None = None
    calls: int = 0
    tokens_in: int = 0
    tokens_out: int = 0
    log: list[dict[str, Any]] = field(default_factory=list)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def ask(self, prompt: dict[str, Any]) -> str:
        import anthropic

        if self.budget is not None:
            self.budget.take()
        client = anthropic.Anthropic(max_retries=4)
        t0 = time.time()
        with client.beta.messages.stream(
            model=self.model,
            max_tokens=self.max_tokens,
            betas=["server-side-fallback-2026-07-01"],
            fallbacks="default",
            thinking={"type": "adaptive"},
            output_config={"effort": self.effort},
            system=[{"type": "text", "text": SYSTEM, "cache_control": {"type": "ephemeral"}}],
            messages=[{"role": "user", "content": json.dumps(prompt, indent=1, default=str)}],
        ) as stream:
            response = stream.get_final_message()
        text = "".join(b.text for b in response.content if b.type == "text")
        m = re.search(r"```(?:rote)?\s*(.*?)```", text, re.S)
        src = m.group(1).strip() if m else text.strip()
        entry = {"call": None, "model": response.model, "stop_reason": response.stop_reason,
                 "input_tokens": response.usage.input_tokens, "output_tokens": response.usage.output_tokens,
                 "cache_read": getattr(response.usage, "cache_read_input_tokens", 0),
                 "seconds": round(time.time() - t0, 1), "prompt": prompt, "response": text}
        with self._lock:
            self.calls += 1
            entry["call"] = self.calls
            self.tokens_in += response.usage.input_tokens
            self.tokens_out += response.usage.output_tokens
            self.log.append({k: v for k, v in entry.items() if k not in ("prompt", "response")})
            if self.transcript_path:
                with open(self.transcript_path, "a") as f:
                    f.write(json.dumps(entry, default=str) + "\n")
        if response.stop_reason == "refusal":
            raise OracleExhausted("the model declined the request")
        return src
