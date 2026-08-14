# AGENTS.md

This file configures agent behavior for this repository.

## Agent skills

### Issue tracker

GitHub Issues via `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary: needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: CONTEXT.md at root + docs/adr/. See `docs/agents/domain.md`.

## Implementation workflow

1. **Branch** — Never commit directly to `main`. Create a feature branch: `git checkout -b implement-<slug>`.
2. **Commit** — Commit work to the feature branch with clear messages.
3. **PR** — Push the branch and open a PR with `gh pr create`. Link the parent spec issue and relevant tickets in the body (e.g. `Closes #2, Closes #3`). Apply the `ready-for-agent` label if the PR is agent-grabbable.
4. **Code review** — Run `/code-review` against `main` (the merge-base). Fix findings on the same branch.
5. **Merge** — Once review passes, merge the PR via `gh pr merge --squash --delete-branch`.
6. **Close tickets** — Issues linked with `Closes #N` auto-close on merge. Manually close any remaining tickets with `gh issue close <N> --comment "Done in #<PR>"`.
7. **Update CONTEXT.md** — If domain terms or decisions changed during implementation, update `CONTEXT.md` and add an ADR if the decision is hard to reverse.