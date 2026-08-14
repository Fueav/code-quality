# v0.5.5 review-scope RED evidence

Date: 2026-08-14 (Asia/Shanghai)

Baseline under test: v0.5.4 implementation plus the approved v0.5.5 specification and contract tests; no v0.5.5 implementation types existed.

Command:

```sh
go test ./quality ./internal/reviewplan
```

Observed exit code: `1`.

Representative failures:

```text
quality/review_identity_v8_contract_test.go:11:27: undefined: ReviewScopeFull
quality/review_identity_v8_contract_test.go:12:16: undefined: BuildReviewIdentity
quality/review_identity_v8_contract_test.go:27:32: undefined: ReviewIdentityInput
internal/reviewplan/reviewplan_contract_test.go:17:19: undefined: Build
internal/reviewplan/reviewplan_contract_test.go:17:47: undefined: Input
internal/reviewplan/reviewplan_contract_test.go:24:24: undefined: quality.ReviewScopeFull
internal/reviewplan/reviewplan_contract_test.go:169:35: undefined: quality.NativeReviewContract
```

This RED state proves the v0.5.4 code has no FULL/INCREMENTAL domain, no deterministic review identity, no versioned provider contract, and no frozen review-plan module capable of accepting explicit base/head refs.
