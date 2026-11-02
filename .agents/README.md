# .agents

The agent setup of this repository, read by every coding agent.

<!-- agents-dir:begin -->

## Layout

| Path | What it holds |
| --- | --- |
| `skills/` | Skills, linked one by one into `.claude/skills/`. |

- **Own skills**, written for this repository: `effective-go`, `google-go-style`.
- **Vendored skills**, third-party and pinned by `skills-lock.json`: none. Refresh them with `npx skills update`; never edit one in place without saying so here.

To vendor a skill, install it with `npx skills add <source> --skill <name>` so it lands in `skills/<name>/` and in `skills-lock.json`, then link it: `ln -s ../../.agents/skills/<name> .claude/skills/<name>`.

<!-- agents-dir:end -->
