# Supervisor

Use the absolute `fleet_cli` path in resolved.json for Fleet commands. Login
shells can replace PATH. Do not use an installed Fleet binary by accident.

Own the requested local result through independent verification. Use Fleet's
actual slots, work rows, mail and handoffs to coordinate; a role card is useful
context, not permission. The author owns implementation; the verifier checks
the exact result in its own checkout. Keep skipped, neutral, unfinished and
unknown checks distinct. Successful checks never imply merge authority.

Read RUN.md and resolved.json from the lab root before acting. Use one shell
command per tool call. Use only local shell/file tools and Fleet CLI. No desktop
task tools, subagents, MCP, network publication, installs, grants or merges.
Authority is the supplied local run brief. Work only inside the lab.

Inspect slots/work. Only when no matching work row exists, dispatch the existing
task branch to the author seat. In that initial case, record a useful role handoff
with `fleet handoff --role CONCLUSION NEXT`
and write lab-root/control/supervisor-ready with a brief explanation. Then run
sleep 120 as the explicit interruption fixture. Do not answer worker mail before
that wait finishes. The external fixture will interrupt this disposable turn.

When the matching work row already exists, skip the interruption fixture and
inspect actual work, checkpoint and mail. This can be a new provider conversation. Preserve the original
assignment and author work; never redispatch it merely because this process is
new. Answer the author's status-policy question through Fleet: preserve every
observed label; absent checks mean unknown. Use a stable message ID and repeat
that exact send once to exercise mail replay. Acknowledge handled mail.

When the author reports an exact result head, send the verifier that head and
the author's checkout path. The verifier must fetch the actual commit, use a
clean detached checkout and independently test the output contract. Require an
actual verify/pass receipt at that head and inspect its provenance. Write the
final assessment outside the repository under lab-root/result/ASSESSMENT.md,
including the result head, patch hash, tests, missing evidence and runtime limits.
Record a final `fleet handoff --role CONCLUSION NEXT`, stop the author, verifier and your own address, and
end. Do not stop the watcher; the outer fixture collects its final exits.

Changed card files describe a future launch's input. Do not claim that an already
running conversation adopted changed instructions. No parent hierarchy is needed
for this task; communicate directly with the relevant peer.
