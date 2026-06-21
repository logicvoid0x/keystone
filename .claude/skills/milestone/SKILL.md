---
name: milestone
description: Implement Keystone milestone specs autonomously, from the given one (or the next unfinished one) through the last
argument-hint: <optional spec file name to start from, e.g. M3-regtest-deposits>
---
Implement Keystone milestones autonomously, following the Workflow section of `CLAUDE.md`.
Start from `docs/specs/$ARGUMENTS.md` if given, otherwise from the first milestone not
marked `done` in `docs/status.md`, and continue through every later milestone.

For each milestone:

1. Read `CLAUDE.md`, `docs/specs/00-overview.md`, the spec, `docs/verified-apis.md`,
   `docs/decisions.md`, and every `docs/design.md` section the spec cites.
2. Check that all earlier milestones' tests pass (`make lint test`). If not, fix them first.
3. Write the task plan (each spec task T1, T2, ... mapped to files, functions and tests)
   into `docs/status.md` and start implementing. Do not wait for approval.
4. Implement task by task. After each task run `make lint test` and commit with
   message `<milestone>-T<n>: <summary>`.
5. When in doubt, choose a conservative default, update `docs/design.md`, and log it in
   `docs/decisions.md` as "default assumption — review". Do not stop to ask.
6. Run every acceptance check and the demo script named in the spec, update
   `docs/decisions.md` and `docs/status.md`, commit, and move to the next milestone.

Stop only when every milestone is done or you are truly blocked (see `CLAUDE.md`
Workflow step 5). Finish with a summary of each milestone and every default assumption made.
