# v0.5.6 RED evidence

Baseline: `v0.5.5` / `8380e9d4e7cb452395b49b479ab15fd8adf802e5`.

Before implementation, `go test ./...` failed the new integration-contract tests:

- `TestReusableCIWorkflowPublishesConciseReleaseGateForBothProviders`: the workflow defaulted to low and did not pass one resolved model/effort/profile to plan, doctor, and run.
- `TestReadmeSeparatesPersonalAndLinuxCIOnboarding`: current onboarding still pinned v0.5.5 and low effort.
- `TestJenkinsUsesOneMaxEffortContractForPlanDoctorAndRun`: Jenkins omitted plan and used doctor max versus run high.
- `TestCompanyCIVersionsExternalRunnerPolicyOutsideCLIResults`: no explicit runner-policy cache boundary existed.
- `TestPluginSkillUsesThinNativeReviewPath`: the Skill did not require a shared contract argument set or point to external runner-policy versioning.

After implementation, the focused contract tests and `go test ./...` pass. Final release-gate and public-asset evidence is recorded only after those commands complete.
