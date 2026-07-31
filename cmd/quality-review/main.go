package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/internal/codexreview"
	evalrunner "github.com/Fueav/code-quality/internal/eval"
	"github.com/Fueav/code-quality/internal/intake"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

var version = "dev"
var codexBinary = "codex"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "quality-review: run `quality-review run-codex` for the default native review")
		fmt.Fprintln(stderr, "quality-review: offline and legacy commands: prepare, finalize, adjudicate, compare, eval, replay, validate, render")
		return 2
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: quality-review version")
			return 2
		}
		fmt.Fprintf(stdout, "quality-review %s\n", version)
		return 0
	case "run-codex":
		return runCodex(args[1:], stdout, stderr)
	}
	policy, err := loadPolicy()
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: load embedded policy: %v\n", err)
		return 2
	}
	switch args[0] {
	case "prepare":
		return runPrepare(args[1:], stdout, stderr)
	case "finalize":
		return runFinalize(args[1:], policy, stdout, stderr)
	case "adjudicate":
		return runAdjudicate(args[1:], policy, stdout, stderr)
	case "compare":
		return runCompare(args[1:], stdout, stderr)
	case "eval":
		return runEval(args[1:], policy, stdout, stderr)
	case "replay":
		return runReplay(args[1:], policy, stdout, stderr)
	case "validate":
		return runValidate(args[1:], policy, stdout, stderr)
	case "render":
		return runRender(args[1:], policy, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "quality-review: unknown command %q\n", args[0])
		return 2
	}
}

type codexRunSummary struct {
	SchemaVersion  int    `json:"schema_version"`
	Status         string `json:"status"`
	SemanticResult string `json:"semantic_result"`
	SessionDir     string `json:"session_dir"`
	ResultPath     string `json:"result_path"`
	MarkdownPath   string `json:"markdown_path"`
	MetricsPath    string `json:"metrics_path"`
	ModelCalls     int    `json:"model_calls"`
}

func runCodex(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run-codex", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repository := flags.String("repo", ".", "Git repository path")
	base := flags.String("base", "", "base commit")
	target := flags.String("target", "", "target commit")
	reason := flags.String("diff-reason", "", "diff selection reason")
	goal := flags.String("goal", "", "optional change intent or extra review concern")
	model := flags.String("model", "", "optional Codex model override")
	reasoningEffort := flags.String("reasoning-effort", "high", "Codex reasoning effort")
	outputRoot := flags.String("output-root", ".code-quality", "session output root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "quality-review: run-codex accepts flags only")
		return 2
	}
	discovered, err := intake.Discover(intake.Options{
		RepositoryPath: *repository,
		Base:           *base,
		Target:         *target,
		DiffReason:     *reason,
	})
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: resolve review scope: %v\n", err)
		return 1
	}
	root, err := resolvePath(*outputRoot, discovered.RepositoryRoot)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: output root: %v\n", err)
		return 2
	}
	prepared, err := reviewsession.PrepareNative(context.Background(), reviewsession.Options{
		RepositoryRoot: discovered.RepositoryRoot,
		OutputRoot:     root,
		Host:           "codex",
		Request:        discovered.Request,
		DirtyWorktree:  discovered.DirtyWorktree,
	})
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: prepare native review: %v\n", err)
		return 1
	}
	if discovered.DirtyWorktree {
		fmt.Fprintln(stderr, "quality-review: working tree changes are not included; review covers committed base and target only")
	}
	result, runErr := codexreview.Run(context.Background(), codexreview.Options{
		Prepared:                prepared,
		Request:                 discovered.Request,
		Goal:                    *goal,
		Model:                   *model,
		ReasoningEffort:         *reasoningEffort,
		EvaluationRubricVersion: quality.EvaluationRubricVersion,
		CodexBinary:             codexBinary,
	})
	if runErr != nil {
		_ = reviewsession.CleanupPreparedCheckout(discovered.RepositoryRoot, prepared)
		fmt.Fprintf(stderr, "quality-review: run native review: %v\n", runErr)
		return 1
	}
	if err := codexreview.Publish(prepared, result); err != nil {
		_ = reviewsession.CleanupPreparedCheckout(discovered.RepositoryRoot, prepared)
		fmt.Fprintf(stderr, "quality-review: publish native review: %v\n", err)
		return 1
	}
	if err := reviewsession.CleanupPreparedCheckout(discovered.RepositoryRoot, prepared); err != nil {
		fmt.Fprintf(stderr, "quality-review: warning: %v\n", err)
	}
	status := "COMPLETE"
	exitCode := 0
	if result.Adjudication.SemanticResult == quality.ResultIncomplete {
		status = "INCOMPLETE"
		exitCode = 1
	}
	summary := codexRunSummary{
		SchemaVersion: 3, Status: status, SemanticResult: result.Adjudication.SemanticResult,
		SessionDir: prepared.SessionDir, ResultPath: prepared.ResultPath, MarkdownPath: prepared.MarkdownPath,
		MetricsPath: prepared.NativeMetricsPath,
		ModelCalls:  result.Execution.ModelCalls,
	}
	if err := quality.EncodeJSON(stdout, summary); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode native review summary: %v\n", err)
		return 2
	}
	return exitCode
}

func runCompare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	productPath := flags.String("product", "", "product finding set JSON")
	baselinePath := flags.String("baseline", "", "externally supplied baseline finding set JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*productPath) == "" || strings.TrimSpace(*baselinePath) == "" {
		fmt.Fprintln(stderr, "usage: quality-review compare --product <findings.json> --baseline <findings.json>")
		return 2
	}
	product, err := decodeFile[evalrunner.FindingSet](*productPath)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: compare product findings: %v\n", err)
		return 1
	}
	baseline, err := decodeFile[evalrunner.FindingSet](*baselinePath)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: compare baseline findings: %v\n", err)
		return 1
	}
	report, err := evalrunner.CompareFindings(product, baseline)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: compare: %v\n", err)
		return 1
	}
	return encodeJSON(stdout, stderr, report)
}

func runPrepare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repository := flags.String("repo", ".", "Git repository path")
	base := flags.String("base", "", "base commit")
	target := flags.String("target", "", "target commit")
	reason := flags.String("diff-reason", "", "diff selection reason")
	host := flags.String("host", "", "host Agent runtime: claude-code or codex")
	outputRoot := flags.String("output-root", ".code-quality", "session output root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "quality-review: prepare accepts flags only")
		return 2
	}
	result, err := intake.Discover(intake.Options{
		RepositoryPath: *repository,
		Base:           *base,
		Target:         *target,
		DiffReason:     *reason,
	})
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: prepare: %v\n", err)
		return 1
	}
	root, err := resolvePath(*outputRoot, result.RepositoryRoot)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: output root: %v\n", err)
		return 2
	}
	prepared, err := reviewsession.Prepare(context.Background(), reviewsession.Options{
		RepositoryRoot: result.RepositoryRoot,
		OutputRoot:     root,
		Host:           *host,
		Request:        result.Request,
		DirtyWorktree:  result.DirtyWorktree,
	})
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: prepare session: %v\n", err)
		return 1
	}
	if result.DirtyWorktree {
		fmt.Fprintln(stderr, "quality-review: working tree changes are not included; review covers committed base and target only")
	}
	if err := quality.EncodeJSON(stdout, prepared); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode prepared session: %v\n", err)
		return 2
	}
	return 0
}

func runFinalize(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("finalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sessionDir := flags.String("session", "", "prepared review session directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sessionDir) == "" {
		fmt.Fprintln(stderr, "usage: quality-review finalize --session <directory>")
		return 2
	}
	result, err := reviewsession.Finalize(reviewsession.FinalizeOptions{SessionDir: *sessionDir}, policy)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: finalize: %v\n", err)
		return 1
	}
	if err := quality.EncodeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode finalize status: %v\n", err)
		return 2
	}
	return 0
}

func runAdjudicate(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("adjudicate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	host := flags.String("host", "", "host Agent runtime: claude-code or codex")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 || (*host != "claude-code" && *host != "codex") {
		fmt.Fprintln(stderr, "usage: quality-review adjudicate --host <claude-code|codex> <request.json> <model-review.json>")
		return 2
	}
	request, requestErr := decodeFile[quality.ReviewRequest](flags.Arg(0))
	review, reviewErr := decodeModelReviewFile(flags.Arg(1))
	if requestErr != nil || reviewErr != nil {
		reasons := []string{}
		if requestErr != nil {
			reasons = append(reasons, "invalid review request: "+requestErr.Error())
		}
		if reviewErr != nil {
			reasons = append(reasons, "invalid model review: "+reviewErr.Error())
		}
		return encodeResult(stdout, stderr, quality.IncompleteResult(request, policy, reasons...))
	}
	review.Execution = quality.Execution{Host: *host, SkillVersion: quality.SkillVersion, AgentCount: 1}
	return encodeResult(stdout, stderr, quality.Adjudicate(request, review, policy))
}

func runEval(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	casesPath := flags.String("cases", "evals/cases.json", "deterministic eval case manifest")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "quality-review: eval accepts flags only")
		return 2
	}
	manifest, err := evalrunner.LoadManifest(*casesPath)
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: load eval cases: %v\n", err)
		return 2
	}
	report := evalrunner.RunDeterministic(manifest, policy)
	if err := quality.EncodeJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode eval report: %v\n", err)
		return 2
	}
	if report.FailedCases > 0 || !report.MatrixComplete || !report.AllRulesReportOnly {
		return 1
	}
	return 0
}

func runReplay(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: quality-review replay <record|summarize> ...")
		return 2
	}
	switch args[0] {
	case "record":
		flags := flag.NewFlagSet("replay record", flag.ContinueOnError)
		flags.SetOutput(stderr)
		caseID := flags.String("case-id", "", "eval case id")
		casesPath := flags.String("cases", "evals/cases.json", "eval case manifest")
		host := flags.String("host", "", "claude-code or codex")
		runNumber := flags.Int("run-number", 1, "replay run number")
		resultPath := flags.String("result", "", "validated review-result.json")
		humanStatus := flags.String("human-status", "pending", "pending, confirmed, or overturned")
		overturnReason := flags.String("overturn-reason", "", "required only when overturned")
		inputTokens := flags.Int("input-tokens", -1, "host-reported input tokens; provide with output tokens and duration")
		outputTokens := flags.Int("output-tokens", -1, "host-reported output tokens; provide with input tokens and duration")
		durationMS := flags.Int("duration-ms", -1, "host-observed wall duration; provide with token metrics")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if flags.NArg() != 0 || *caseID == "" || *host == "" || *resultPath == "" {
			fmt.Fprintln(stderr, "usage: quality-review replay record --case-id <id> --host <claude-code|codex> --result <review-result.json> [--run-number N] [--human-status <status>]")
			return 2
		}
		result, err := decodeReviewResultFile(*resultPath)
		if err != nil {
			fmt.Fprintf(stderr, "quality-review: replay record: %v\n", err)
			return 1
		}
		if validationErrors := quality.ValidateResult(result, policy); len(validationErrors) > 0 {
			fmt.Fprintf(stderr, "quality-review: replay record: %s\n", strings.Join(validationErrors, "; "))
			return 1
		}
		if result.Execution.Host != *host {
			fmt.Fprintf(stderr, "quality-review: replay record: --host %s does not match result execution host %s\n", *host, result.Execution.Host)
			return 1
		}
		var reason *string
		if strings.TrimSpace(*overturnReason) != "" {
			value := strings.TrimSpace(*overturnReason)
			reason = &value
		}
		record := evalrunner.RecordFromResult(*caseID, *host, *runNumber, result, evalrunner.HumanReview{Status: *humanStatus, OverturnReason: reason})
		if err := applyReplayMetrics(&record, *inputTokens, *outputTokens, *durationMS); err != nil {
			fmt.Fprintf(stderr, "quality-review: replay record: %v\n", err)
			return 2
		}
		manifest, err := loadReplayManifest(*casesPath, policy)
		if err != nil {
			fmt.Fprintf(stderr, "quality-review: replay record: %v\n", err)
			return 1
		}
		report := evalrunner.RunReplay(manifest, policy, []evalrunner.ReplayRecord{record}, nil)
		if report.InvalidRecords > 0 {
			fmt.Fprintf(stderr, "quality-review: replay record: %s\n", strings.Join(report.Errors, "; "))
			return 1
		}
		return encodeJSON(stdout, stderr, record)
	case "summarize":
		flags := flag.NewFlagSet("replay summarize", flag.ContinueOnError)
		flags.SetOutput(stderr)
		casesPath := flags.String("cases", "evals/cases.json", "eval case manifest")
		recordsDir := flags.String("records", "", "directory containing replay JSON records")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if flags.NArg() != 0 || *recordsDir == "" {
			fmt.Fprintln(stderr, "usage: quality-review replay summarize --records <directory> [--cases <cases.json>]")
			return 2
		}
		manifest, err := loadReplayManifest(*casesPath, policy)
		if err != nil {
			fmt.Fprintf(stderr, "quality-review: replay summarize: %v\n", err)
			return 1
		}
		records, loadErrors := evalrunner.LoadReplayRecords(*recordsDir)
		report := evalrunner.RunReplay(manifest, policy, records, loadErrors)
		if code := encodeJSON(stdout, stderr, report); code != 0 {
			return code
		}
		if report.InvalidRecords > 0 || !report.AgentLimitRespected {
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "quality-review: unknown replay command %q\n", args[0])
		return 2
	}
}

func applyReplayMetrics(record *evalrunner.ReplayRecord, inputTokens, outputTokens, durationMS int) error {
	values := []int{inputTokens, outputTokens, durationMS}
	provided := 0
	for _, value := range values {
		if value >= 0 {
			provided++
		} else if value != -1 {
			return errors.New("replay metrics must be non-negative")
		}
	}
	if provided == 0 {
		return nil
	}
	if provided != len(values) {
		return errors.New("input tokens, output tokens, and duration must be provided together")
	}
	record.Observed.InputTokens = &inputTokens
	record.Observed.OutputTokens = &outputTokens
	record.Observed.DurationMS = &durationMS
	return nil
}

func loadReplayManifest(path string, policy quality.PolicyManifest) (evalrunner.Manifest, error) {
	manifest, err := evalrunner.LoadManifest(path)
	if err != nil {
		return evalrunner.Manifest{}, err
	}
	if validationErrors := evalrunner.ValidateManifest(manifest, policy); len(validationErrors) > 0 {
		return evalrunner.Manifest{}, fmt.Errorf("invalid eval manifest: %s", strings.Join(validationErrors, "; "))
	}
	return manifest, nil
}

func encodeJSON(stdout, stderr io.Writer, value any) int {
	if err := quality.EncodeJSON(stdout, value); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode JSON: %v\n", err)
		return 2
	}
	return 0
}

func runValidate(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: quality-review validate <review-result.json>")
		return 2
	}
	result, err := decodeReviewResultFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: invalid result: %v\n", err)
		return 1
	}
	if validationErrors := quality.ValidateResult(result, policy); len(validationErrors) > 0 {
		for _, message := range validationErrors {
			fmt.Fprintf(stderr, "quality-review: %s\n", message)
		}
		return 1
	}
	fmt.Fprintln(stdout, "review result is valid")
	return 0
}

func runRender(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: quality-review render <review-result.json>")
		return 2
	}
	result, err := decodeReviewResultFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: invalid result: %v\n", err)
		return 1
	}
	if validationErrors := quality.ValidateResult(result, policy); len(validationErrors) > 0 {
		for _, message := range validationErrors {
			fmt.Fprintf(stderr, "quality-review: %s\n", message)
		}
		return 1
	}
	fmt.Fprint(stdout, quality.RenderMarkdown(result))
	return 0
}

func encodeResult(stdout, stderr io.Writer, result quality.ReviewResult) int {
	if err := quality.EncodeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode result: %v\n", err)
		return 2
	}
	return 0
}

func resolvePath(value, repositoryRoot string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	if strings.TrimSpace(repositoryRoot) == "" {
		return "", errors.New("repository root is required for a relative path")
	}
	return filepath.Join(repositoryRoot, filepath.Clean(value)), nil
}

func loadPolicy() (quality.PolicyManifest, error) {
	raw, err := bundle.PolicyManifest()
	if err != nil {
		return quality.PolicyManifest{}, err
	}
	return quality.DecodeStrict[quality.PolicyManifest](bytes.NewReader(raw))
}

func decodeModelReviewFile(path string) (quality.ModelReview, error) {
	file, err := os.Open(path)
	if err != nil {
		return quality.ModelReview{}, err
	}
	defer file.Close()
	return quality.DecodeModelReview(file)
}

func decodeReviewResultFile(path string) (quality.ReviewResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return quality.ReviewResult{}, err
	}
	defer file.Close()
	return quality.DecodeReviewResult(file)
}

func decodeFile[T any](path string) (T, error) {
	file, err := os.Open(path)
	if err != nil {
		var zero T
		return zero, err
	}
	defer file.Close()
	return quality.DecodeStrict[T](file)
}
