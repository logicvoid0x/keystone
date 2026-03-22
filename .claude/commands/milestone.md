---
description: Plan and implement one Keystone milestone spec
argument-hint: <spec file name, e.g. M1-oracle-vaults-liquidations>
---
Implement milestone spec `docs/specs/$ARGUMENTS.md`.

1. Read `CLAUDE.md`, `docs/specs/00-overview.md`, `docs/specs/$ARGUMENTS.md`,
   `docs/verified-apis.md`, `docs/decisions.md`, and every `docs/design.md`
   section the spec cites.
2. Check that all earlier milestones' tests pass (`make lint test`). If not, stop and report.
3. Enter plan mode. Produce a plan that maps each spec task (T1, T2, ...) to files,
   functions and tests, and lists any API you still need to confirm in the module cache.
   Wait for my approval.
4. After approval, implement task by task. After each task run `make lint test`
   and commit with message `<milestone>-T<n>: <summary>`.
5. Finish by running every acceptance check and the demo script named in the spec.
   Report results, update `docs/decisions.md`, and stop.
