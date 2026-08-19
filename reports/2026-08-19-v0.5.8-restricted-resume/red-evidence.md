# v0.5.8 restricted resume RED evidence

Baseline: `2ae1f4b` (`docs: specify v0.5.8 restricted resume`).

Command:

```sh
go test ./internal/nativereview .
```

Observed RED result: exit code 1.

The recovery suite failed to compile because the baseline had no persistent `TransactionResult.Status`, `RESTRICTED_RETRYABLE` state, `ResumeRestricted` entry point, or `ResumeOptions`. Representative diagnostics:

```text
internal/nativereview/resume_test.go:20:38: initial.Status undefined
internal/nativereview/resume_test.go:20:54: undefined: StateRestrictedRetryable
internal/nativereview/resume_test.go:39:18: undefined: ResumeRestricted
internal/nativereview/resume_test.go:39:57: undefined: ResumeOptions
```

The bundle contract suite also proved that the baseline had none of the required versioned wire artifacts:

```text
TestNativeResultV10SchemaCarriesStageAttemptAccounting: review-result-v10.schema.json: file does not exist
TestCompanyCIEnvelopeV3ReferencesOnlyResultV10: review-result-envelope-v3.schema.json: file does not exist
```

The tests additionally freeze the current v8/v9 and envelope-v1/v2 SHA-256 values so implementation cannot satisfy v0.5.8 by mutating an already published schema.

## GREEN evidence

Implementation verification was run from the isolated feature worktree after the full checkpoint, recovery, tamper, crash, idempotency, concurrency, Codex, and Claude regression matrix was present.

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
AI_BOUNDARY_APPROVED=1 \
AI_BOUNDARY_APPROVAL_EVIDENCE=owner-request:v0.5.8-restricted-resume \
VERIFY_COMPARE_REF=ee1cd6d \
make release-check VERSION=v0.5.8
AI_BOUNDARY_APPROVED=1 \
AI_BOUNDARY_APPROVAL_EVIDENCE=owner-request:v0.5.8-restricted-resume \
VERIFY_COMPARE_REF=ee1cd6d \
make verify-change
```

All commands exited 0. `release-check` included 34 qualification tests, 8 live tests, 2 mining tests, shell syntax checks, formatting, and diff checks. `verify-change` passed `change_scope`, `ai_boundaries`, `project_test`, and `repository_modules` and wrote Harness evidence under `.artifacts/change`.

Every `schemas/*.schema.json` compiled with a local JSON Schema draft 2020-12 validator. The v3 company envelope fixture validated against `review-result-envelope-v3.schema.json`. Cross-compilation also succeeded for darwin/arm64, darwin/amd64, linux/amd64, and linux/arm64.

The separate company service candidate passed `go test -race -count=1 ./...` and `go vet ./...`. Its integration regression `TestGitHubRerunUsesOnlyRestrictedResume` proves that the same base/head rerun issues exactly one `resume-restricted` command, requests no checkout token, records `Native Review reused`, and publishes a 1 Native + 2 Restricted attempt ledger. `TestRestrictedSessionResumeUsesIdentityCAS` proves one CAS owner and terminal `MANUAL_REQUIRED` behavior after the second Restricted failure. The service candidate was not deployed or restarted.
