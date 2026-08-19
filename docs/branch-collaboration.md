# Branch Collaboration

This file is the single source of truth for branch and worktree collaboration.

## Branch Model

- Stable and integration branch: `main`.
- Owner task branches use `chris/<topic>`; shared task branches may use `feature/*`, `fix/*`, or `chore/*`.
- Release tags identify published versions; moving or replacing a published tag is forbidden.

## Flow

1. **Isolate:** Fetch `origin/main`, then create one temporary task branch in one linked worktree without switching or modifying the user's current checkout.
2. **Develop:** Keep changes scoped to that worktree. Intentional review, policy, schema, provider, or release semantics follow an approved project specification.
3. **Verify:** Run `make verify-change` while the tree may be dirty. Commit the candidate, then run `VERIFY_COMPARE_REF=origin/main make verify-candidate`. Before opening a pull request, run the installed Code Quality against the exact clean range from `git merge-base origin/main HEAD` to `HEAD`; only `PASS` permits PR creation, while `BLOCK` or `ERROR` stops.
4. **Integrate:** Merge only a verified branch and never force-push. Run `make verify-release` once for the exact final release SHA, or verify and reuse valid evidence when that SHA and tree are unchanged.
5. **Clean:** After successful integration, automatically remove the task worktree and local/remote task branches only when every cleanup condition below passes.

## Automatic Cleanup Contract

Cleanup needs no further owner confirmation only when Git proves all of the following:

- the branch uses an allowed temporary prefix and is neither the current, stable, integration, nor another protected branch;
- the linked worktree is not current and has no staged, unstaged, or untracked changes;
- after fetching the remote, the local task HEAD and remote task HEAD, when present, are ancestors of `origin/main`.

If any proof is missing or fails, preserve all state and report the blocker. Unmerged abandoned work requires an explicit owner decision.
