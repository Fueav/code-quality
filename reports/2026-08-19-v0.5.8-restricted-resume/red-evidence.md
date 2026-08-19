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
