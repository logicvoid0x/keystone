# Keystone spec pack

Drop these files at the root of an empty Git repository, then build Keystone with
Claude Code one milestone at a time.

| Path | Purpose |
| --- | --- |
| `CLAUDE.md` | Rules Claude Code loads automatically: versions, determinism rules, workflow |
| `.claude/skills/milestone/SKILL.md` | `/milestone [spec]` skill: implements all remaining milestones autonomously |
| `docs/design.md` | Full design document (exported from the shared doc) |
| `docs/verified-apis.md` | SDK v0.55.0 / CometBFT v0.40.0 / btcd APIs checked against source |
| `docs/decisions.md` | Decision log Claude Code appends to |
| `docs/specs/00-overview.md` | Milestones, repo layout, conventions, definition of done |
| `docs/specs/M0 … M6` | One spec per milestone |

## Steps

1. `git init keystone && cd keystone`, copy this pack in, replace `OWNER` in the specs
   with your GitHub user or org, commit.
2. Install Go 1.27.3 (or let `GOTOOLCHAIN=auto` fetch it), Docker, and `buf`.
3. Start Claude Code in the repo and run `/milestone M0-scaffold`. Review the plan it
   proposes, approve, and let it implement and test. Review the diff, then start a new
   session (or `/clear`) for the next milestone.
4. The short demo is M0 → M1 → M2. Real Bitcoin custody is M3 → M6.
