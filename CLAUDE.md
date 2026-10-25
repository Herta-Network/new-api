# CLAUDE.md — Project Conventions for new-api

## MANDATORY: Read AGENTS.md with the Read tool

Do not treat `@AGENTS.md` as loaded. Claude Code does not reliably inline that import.

Before any planning, coding, reviewing, or answering a project question, you MUST call the Read tool on the repo-root file `AGENTS.md` and wait for the full contents. This is the first action of every session and every new task.

Rules:

- Do not start from memory, summaries, or this file alone.
- Do not skip the Read because a previous turn mentioned AGENTS.md.
- Do not replace the Read with a grep, glob, or partial skim.
- After reading, follow every rule in `AGENTS.md` for the rest of the work.
- The web console is the separate repository `Herta-Network/new-api-frontend`; frontend work happens there, under its own `AGENTS.md`. This repository has no `web/` directory.
- If the task touches billing as defined under **Billing rules (mandatory read gate)** in `AGENTS.md`, also Read `.agents/rules/billing.md` in full before planning or editing. Tasks outside that definition may skip it.
- If the task syncs, merges, or ports changes from upstream `QuantumNous/new-api`, also Read `.agents/rules/upstream-sync.md` in full before fetching or resolving anything (see **Upstream sync (mandatory read gate)** in `AGENTS.md`).
