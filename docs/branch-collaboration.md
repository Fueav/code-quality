# Branch Collaboration

## Branches

Stable and integration branch: `main`. Temporary task prefix: `chris/`; existing `feature/`, `fix/`, and `chore/` branches remain supported. Projects with another integration branch replace that fact here.

## Work And Integration

- Inspect current branch and dirty state. Reuse a suitable task worktree; create an isolated branch from the current remote integration branch when the current checkout is shared, protected, or contains conflicting user work. Never overwrite unrelated changes.
- Run relevant checks and inspect the diff. Ordinary changes need no separate AI review before opening a PR. Public contracts, funds, security boundaries, destructive data operations and protected deployment changes require independent review plus the applicable owner approval before integration.
- Merge only a verified candidate. Run full verification for actual release promotion; reuse valid evidence only with unchanged commit, baseline and verification inputs. Do not repeat verification solely because a ref moved; never force-push.
- After integration, remove only a non-current temporary task worktree and branches that are clean (including untracked files), unprotected, and proven ancestors of the fetched remote integration HEAD. Preserve abandoned or unmerged work unless the owner authorizes deletion.
