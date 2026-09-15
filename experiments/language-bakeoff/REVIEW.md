# Workbench language bakeoff: utility review

> Superseded direction: the operator clarified that the useful input
> is a small declaration of Fleet cards/seats and possibly compute, not a
> general-purpose program. The typed-Go selection below overvalued flexibility.
> The active follow-up is [Terraform + Fleet + Rooms](../terraform-fleet/README.md).
> Preserve the analysis below as history,
> not as the current recommendation. Test declarative setup first; add a custom
> Fleet provider only for demonstrated lifecycle/drift needs.

Reviewed 2026-09-14. This review asks whether any entry is useful enough to
advance, especially for capable agents. It selects the next experiment without
filling the operator's numeric scores; [SCORECARD.md](SCORECARD.md) remains
blank.

## Verdict

**Pick the typed Go API as the best entry and advance it as a Rooms experiment,
without adopting it as Workbench's language.** Keep direct tools as the default
and the small JSON plan/apply boundary from Workbench
[#337](https://github.com/itsHabib/workbench/pull/337) as the trusted effect
side of the experiment.

All three entries work. None demonstrates that its *representation* makes a
capable agent materially more effective. Their useful behavior comes from the
same underlying machinery:

- normalized intent with explicit dependencies;
- a readable plan before effects;
- refusal when observed state changed after planning;
- per-action results and evidence that survive a fresh caller.

Those are runtime and review properties, not language properties. A capable
agent can produce JSON or call direct tools without needing HCL, a new grammar,
or Go-as-configuration. The human benefits from the rendered plan, not primarily
from the source syntax the agent used to produce it.

## Utility by entry

| Entry | Actual utility | Who benefits | Why it falls short | Disposition |
|---|---|---|---|---|
| HCL | Familiar blocks, comments, heredocs, source locations and extracted references make a long-lived hand-edited configuration easier to review. It is the only entry with a credible user-facing syntax advantage. | A human who repeatedly owns and edits a multi-resource manifest. A capable agent gains little beyond standard syntax and better parser diagnostics. | The useful planner still requires about 3,000 non-test Go lines, including an 821-line application compiler. HCL adds six linked modules and a large expression language that Workbench must restrict. Nothing shows that people will frequently hand-edit these declarations. | **Shelve, do not adopt.** Reconsider only after actual human-authored manifests become painful in JSON. |
| Purpose-built `wb` | `keep` versus `run` communicates lifecycle unusually well at a glance. | A first-time reader of the demo. | That clarity can be shown in the rendered plan. It does not justify owning 763 lines of lexer/parser/checker plus future formatting, editor, compatibility and teaching costs. The custom syntax enables no capability the other entries lack. | **Stop here.** This is the weakest practical bet despite being a good POC. |
| Typed Go API | Strongest way for Workbench developers to compose reusable declarations and add adapters with ordinary types, functions and tooling. It is the smallest entry and avoids a parser. | An engineer building a library or reusable internal workflow. | It is poor trusted-host configuration: evaluating source executes arbitrary Go, abstractions can hide intent, every change needs a toolchain/rebuild, and non-programmers cannot safely edit it. The reviewed artifact still needs to be emitted JSON plus a plan. | **Selected best entry. Advance as Rooms experiment #14:** evaluate untrusted typed source inside a Room, return hostile intent data, then validate and render outside. Do not give the guest backend authority. |
| Direct tools | Fastest path for a capable agent handling one bounded task. The baselines already converge, avoid repeated effects, follow input changes and retry partial failure correctly. | Agents and engineers doing bespoke or low-frequency work. | No pre-effect plan boundary; therefore no stale-plan refusal or uniform explanation. Extension edits the procedure directly. | **Keep as the default.** Add structure only where the missing review/recovery behavior is demonstrably costly. |

There is no demonstrated external buyer for any entry. The credible internal
buyer is a Workbench operator who repeatedly applies several related changes and
needs to review the exact proposal, survive interruption and distinguish
committed work from completion. That buyer needs the shared plan contract; they
do not yet need a language.

## What the six cases actually teach

Cases 1, 2, 3 and 5 do not justify a language. The direct baselines handle
initial apply, unchanged reapply, downstream input changes and partial retry.
The declarative entries explain those operations better, but explanation alone
does not justify thousands of lines of framework.

Case 4 is the strongest result: a reviewed plan becomes stale after an external
edit, so all three entries refuse to apply it. That is a real safety and trust
improvement when a human approves an agent-authored operation.

Case 6 shows that a generic adapter boundary can prevent orchestration from
becoming a growing switch statement. That becomes valuable only when Workbench
actually has several stable backends or resource kinds. Three toy filesystem
adapters prove extensibility, not demand for it.

The shared journals help a fresh caller understand failed or interrupted work,
but they also create new persistent state, at-least-once replay semantics and
another truth surface. In Workbench, Fleet and Rooms should retain their own
facts and effect history; a language client must not duplicate them.

## Fit with roles, slots, Fleet and Rooms

Editable role cards are prose sources for purpose, responsibilities and useful
context. Named slots are capacity and bindings. Work records are assignments.
Fleet owns headless launch, occupancy, mail, checkpoints, stop and receipts.
Rooms owns isolated execution and lifecycle evidence.

A useful deployment description can reference these things, resolve exact card
bytes and source revisions, and render the derived role/tenant/seat/checkout/
provider diff. It should then call the existing backends and report their facts.
It should not introduce another role registry, hierarchy, supervisor, lease
model, journal or authority layer.

Current Workbench evidence argues for that smaller design:

- [#337](https://github.com/itsHabib/workbench/pull/337) already demonstrates
  JSON intent → readable plan → exact digest → conditional, replayable apply for
  unseated work, using Fleet's canonical row rather than a second journal.
- [#340](https://github.com/itsHabib/workbench/pull/340) found no material Fleet
  advantage over direct tools for a desktop recovery workload. More machinery
  must therefore prove value on a harder operational case.
- [#344](https://github.com/itsHabib/workbench/pull/344) demonstrates copied
  editable card sources, resolved configuration, inspection and explicit update,
  plus real headless Fleet coordination and optional Rooms verification—without
  a general language.

## Selected experiment

The typed entry is the right winner for an experiment because Rooms directly
addresses its most important weakness. Running arbitrary Go on the trusted host
is a bad configuration boundary; running it in a disposable, no-egress Room with
no backend credentials may turn it into a useful programmable intent compiler.
HCL's safety advantage matters less in that topology, while the custom language
still contributes no unique capability.

The experiment was also registered as Rooms experiment 14 at local commit
`6dee38a`, with a falsifiable plan for compiling typed intent inside a Room.

Preserve this product boundary:

1. An agent or human submits small structured intent, provisionally JSON.
2. Workbench resolves references and source provenance into normalized data.
3. A renderer shows exact proposed effects, their origins and relevant observed
   state.
4. Apply binds to that reviewed digest, refuses stale facts, and returns
   per-action committed/conflict/unknown outcomes.
5. Fleet and Rooms remain the authorities for execution state and evidence.

The first Rooms run should compare direct normalized JSON against the
`textpipe` + `scratchpipe` typed composition, repeat it in fresh Rooms, and attack
the returned intent with forged/case-variant references. The current typed head
has a known deferred P2 in strict JSON reference decoding, so the host validator
must reject that mutant or the experiment fails immediately. Merely running the
Go tests inside a Room would duplicate the existing Rooms CI experiment and does
not answer the utility question.

If the typed source does not make a real composed workload clearer or easier to
maintain than JSON, stop. If hostile intent reaches planning, stop. If it passes,
the next phase may feed validated intent into a disposable #337 renderer before
any separately authorized backend apply. HCL remains the fallback only if later
human editing trials show concrete pain; do not continue the custom language.

## Evidence used

- [Common bakeoff contract](README.md)
- [HCL README](entries/hcl/README.md)
- [Purpose-built language README](entries/custom/README.md)
  and [review record](entries/custom/REVIEW.md)
- [Typed API README](entries/typed/README.md)
- [Deployment-manifest use case](deployment-manifest-use-case.md)
- [Terraform + Fleet + Rooms follow-up](../terraform-fleet/README.md)

All three demos and direct-baseline smoke tests passed during the preceding
verification. That establishes behavior on the canned workload, not practical
adoption value. No Fleet/Rooms integration was inferred from those tests.
