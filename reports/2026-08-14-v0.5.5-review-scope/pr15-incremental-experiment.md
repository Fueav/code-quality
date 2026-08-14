# PR 15 incremental Codex Exec experiment

Date: 2026-08-14 (Asia/Shanghai)

This experiment tested the architectural boundary before release: `quality-review` resolves refs and prepares a deterministic commit range; Codex Exec receives a detached checkout, an explicit delta, the prior blocker, and a structured output schema. Codex Exec itself receives no base/head branch option.

## Frozen inputs

- Repository: `moss-site/agent_marketplace`
- Direction: `fix/chris-owner-transfer-reconcile` into `production`
- Observed destination tip: `cbf00136f4b67675e0debd3c40837ead8173cd45`
- Full PR merge-base: `d95ad396add864a642f7971d25e371b038ba8c36`
- Previous reviewed head: `17700ad41ed4235da1e7a5952bebe4d26d97ad03`
- Current head: `8e138c68a16f09169ea867e96d7fa04e3a362756`
- Incremental range: 3 commits, 6 changed files
- Previous blocker: one P1 about applying an Agent-emitted ownership event before `AgentCreated`

The six delta files matched `git diff --name-only 17700ad41ed4235da1e7a5952bebe4d26d97ad03..8e138c68a16f09169ea867e96d7fa04e3a362756` exactly.

## Result

The raw Codex Exec structured result was:

```json
{
  "previous_finding_resolutions": [
    {
      "finding_id": "pr15-17700ad-p1-discovery-order",
      "status": "RESOLVED",
      "reason": "The current head removes the causal path by applying AgentCreated before deferred Agent-emitted events.",
      "current_finding": null
    }
  ],
  "new_findings": []
}
```

This agrees with the production full-review conclusion at the same head: the old P1 no longer blocks and no new finding was reported. The experiment therefore supports the design claim that refs do not need to be understood by Codex Exec; deterministic Git preparation in the CLI is sufficient.

Retained temporary evidence at execution time:

| Artifact | SHA-256 |
| --- | --- |
| Incremental output schema | `6ecb4748b212546082195352e77a017a795c7f9fef9d4e0e16fa968630bc8980` |
| Incremental prompt | `d6aebf2bc9796b25b89caccc65c0ac914408ba5d58c25f1945d753771b8a54fb` |
| Structured result | `fab45d287e4a8b054bf58abaf3c8954847b3df39a7d42e9a4269600fc4522856` |

The temporary detached checkout was removed after the run. The original `agent_marketplace` checkout was not modified by the experiment.
