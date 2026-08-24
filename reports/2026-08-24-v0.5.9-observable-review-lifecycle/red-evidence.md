# v0.5.9 observable review lifecycle RED evidence

Status: captured before implementation.

Command:

```text
gofmt -w internal/nativereview/progress_test.go bundle_test.go && go test ./internal/nativereview .
```

Expected RED result:

```text
internal/nativereview/progress_test.go:18:19: undefined: newProgressReporter
internal/nativereview/progress_test.go:18:48: undefined: ProgressFormatJSONL
internal/nativereview/progress_test.go:22:22: undefined: ProgressPlanStarted
...
bundle_test.go:128: open schemas/review-progress-event-v1.schema.json: file does not exist
FAIL github.com/Fueav/code-quality/internal/nativereview [build failed]
FAIL github.com/Fueav/code-quality
```

The failure proves both the behavior seam and the embedded wire contract were absent before implementation.

## GREEN evidence

Focused lifecycle and compatibility tests:

```text
go test ./internal/nativereview ./cmd/quality-review .
ok github.com/Fueav/code-quality/internal/nativereview
ok github.com/Fueav/code-quality/cmd/quality-review
ok github.com/Fueav/code-quality
```

Full native suite and static analysis:

```text
go test ./...
PASS
go vet ./...
PASS
git diff --check
PASS
```

Harness dirty-change profile:

```text
AI_BOUNDARY_APPROVED=1 \
AI_BOUNDARY_APPROVAL_EVIDENCE=owner-request:2026-08-24-v0.5.9-observable-review-lifecycle \
VERIFY_COMPARE_REF=origin/main make verify-change

PASS change_scope
PASS ai_boundaries
PASS project_test
PASS repository_modules
```

Self-review moved `NATIVE_FROZEN` after successful outcome classification and checkpoint persistence, so a Service cannot mistake raw artifact freeze for a resumable checkpoint state.
