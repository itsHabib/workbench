# Goal: lesson plans for the unplanned lectures of MIT 6.006 in ivy

This checkout is a copy of the ivy repository. Its `docs/work-board.md`, job class A, describes
the work exactly, and this goal is that job for one bundle: `6.006` (Introduction to
Algorithms). One lecture, `l09-breadth-first-search`, already has a plan at
`spikes/voice-teacher/lessons/6.006/l09-breadth-first-search.json`; read it first, it is the
model for every plan you write. The other lectures under `bundles/6.006/lectures/` have none.

The team decides the units. The natural unit is one lecture, and lectures are independent: two
seats never need to touch the same file, so claim freely and do not wait on each other.

A plan has one section per chunk. Chunk boundaries are owned by `scripts/lesson.mjs`; run
`node scripts/lesson.mjs outline 6.006 <lecture>` and never re-derive them by eye. Each section
carries the chunk index, the chunk's topic, an emphasis written for a voice teacher (what to
land, in what order, what to say plainly), and, where the material supports one, a quiz whose
correct option is taught by the emphasis in the words the learner will hear. Source defects you
find are corrected at plan level and recorded in the plan's `note` field for this run (the
shared errata file is left alone so two seats never write one file).

Do not render narration: that needs a key this run does not carry. `check-plans` warns about
missing narration; a warning is fine, an error is not.

Verification, from the repository root:

    node scripts/check-plans.mjs 6.006

Done means: every lecture under `bundles/6.006/lectures/` has a plan, the check above reports
zero errors on `main` at `origin`, and each plan's note says what was hard about that lecture.
