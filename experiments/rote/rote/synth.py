"""The oracle seam: the only place an LLM can enter.

`Oracle.ask(prompt)` receives a JSON-like prompt and returns a value; for the
default tactic the value is the source text of a `script` declaration. Two
implementations:

  ScriptedOracle  -- replays a cassette of canned proposals. Every number in
                     RESULTS.md comes from this one. Deterministic, model-free.
  LiveOracle      -- calls Claude through the official SDK. Included so the seam
                     is concrete; it is NOT exercised anywhere in this experiment
                     (no key is assumed) and is labeled as such.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from typing import Any

__all__ = ["ScriptedOracle", "LiveOracle", "OracleExhausted", "GRAMMAR_CARD"]


class OracleExhausted(Exception):
    """The cassette has no further proposal for this capability."""


@dataclass
class ScriptedOracle:
    """Pops the next canned proposal for the capability named in the prompt."""

    cassette: dict[str, list[str]]
    calls: int = 0
    log: list[dict[str, Any]] = field(default_factory=list)
    cursor: dict[str, int] = field(default_factory=dict)

    @staticmethod
    def from_file(path: str) -> ScriptedOracle:
        with open(path) as f:
            data = json.load(f)
        return ScriptedOracle({k: [p["source"] if isinstance(p, dict) else p for p in v] for k, v in data.items()})

    def ask(self, prompt: Any) -> Any:
        self.calls += 1
        cap = prompt.get("cap") if isinstance(prompt, dict) else None
        if cap is None:
            raise OracleExhausted("scripted oracle needs a prompt naming a capability")
        i = self.cursor.get(cap, 0)
        queue = self.cassette.get(cap, [])
        if i >= len(queue):
            raise OracleExhausted(f"no scripted proposal left for {cap!r} (asked {i + 1} times)")
        self.cursor[cap] = i + 1
        self.log.append({"call": self.calls, "cap": cap, "proposal": i, "prompt": prompt})
        return queue[i]


GRAMMAR_CARD = """\
Rote script grammar (whole language):
  script NAME(params) { stmt* }         stmt := let x = expr | for x in expr { ... } | expr
  expr  := literals (ints, "strings", true, false, unit), x, [a, b], {k: v, ...}, x.field, xs[i]
         | f(args) | fn(x, y) { ... } | if c { ... } else { ... } | return e | use CAP(args)
         | a + b, a - b, a * b, a / b, a % b, ==, !=, <, <=, >, >=, and, or, not a
  builtins: len map filter fold all any contains keys has get str range min max fail append
World functions are called by name; `observe` ones read, `act` ones change the world.
A script may not call ask(). Only the goal decides success. Observe before you act.
"""


@dataclass
class LiveOracle:
    """UNEXECUTED in this experiment. A thin adapter from the prompt dict to one Claude call."""

    model: str = "claude-opus-5-5"
    effort: str = "high"
    max_tokens: int = 16000

    def ask(self, prompt: Any) -> Any:
        import anthropic  # optional dependency; only needed for live synthesis

        client = anthropic.Anthropic()
        system = ("You write Rote scripts for an operator's capability library. Reply with exactly one "
                  "```rote fenced block containing a single `script` declaration and nothing else.\n\n" + GRAMMAR_CARD)
        with client.beta.messages.stream(
            model=self.model,
            max_tokens=self.max_tokens,
            betas=["server-side-fallback-2026-07-01"],
            fallbacks="default",
            thinking={"type": "adaptive"},
            output_config={"effort": self.effort},
            system=system,
            messages=[{"role": "user", "content": json.dumps(prompt, indent=1)}],
        ) as stream:
            response = stream.get_final_message()
        if response.stop_reason == "refusal":
            raise OracleExhausted("the model declined the request")
        text = "".join(b.text for b in response.content if b.type == "text")
        m = re.search(r"```(?:rote)?\s*(.*?)```", text, re.S)
        return m.group(1).strip() if m else text.strip()
