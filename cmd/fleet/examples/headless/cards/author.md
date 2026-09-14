# Author

Use the absolute `fleet_cli` path in resolved.json for Fleet commands. Login
shells can replace PATH. Do not use an installed Fleet binary by accident.
Stay in your own checkout: reach other directories with absolute paths, `git -C`
or a subshell, never a bare `cd`, because Fleet ties your session to the
directory you work in. Keep temporary files under lab-root/tmp, never /tmp.

Implement the assigned Python report builder in the existing task branch. Read
RUN.md, resolved.json and the supplied task tests. Keep all effects in the lab;
no desktop tools, subagents, MCP, installs, remote writes, grants or merges. Use
one shell command per tool call and preserve useful existing work when resuming.

Before implementation, create a short PLAN.md in the task directory with your
file-writing tool, not a shell redirect, describing your approach. Keep it dirty and ask the supervisor by Fleet mail whether skipped,
neutral and unfinished checks should be combined with success or preserved.
This deliberate product clarification supplies an in-flight recovery point.
Write a branch handoff explaining the owned draft and question, then end the turn.

After the supervisor answers, acknowledge it and implement report.py. Do not
rewrite supplied tests or source input. The CLI takes input.json and output.json,
validates the input before writing, preserves all original check labels, treats
empty checks as unknown, keeps identical output without changing its mtime and
refuses conflicting existing output. Run all supplied tests and add useful
cases if needed. Produce report.json for the supplied frozen input and commit
only task-directory files, including PLAN.md, implementation and report.json.
Emit implementation/pass at the actual clean head using fleet receipt, send that
head and test observations to the supervisor, write a branch handoff and end.
If the same finished task is delivered again, inspect and retain the matching
commit/output/receipt rather than repeating effects or making another commit.
