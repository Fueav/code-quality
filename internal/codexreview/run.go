package codexreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const maxNativeOutputBytes = int64(10 << 20)

type Options struct {
	Prepared                reviewsession.Prepared
	Request                 quality.ReviewRequest
	Goal                    string
	Model                   string
	ReasoningEffort         string
	EvaluationRubricVersion string
	CodexBinary             string
	Executor                Executor
}

type Invocation struct {
	Executable string
	Args       []string
	Dir        string
	Stdin      string
	OutputPath string
	StdoutPath string
	StderrPath string
}

type Executor interface {
	Run(context.Context, Invocation) error
}

type ProcessExecutor struct{}

func (ProcessExecutor) Run(ctx context.Context, invocation Invocation) error {
	stdout, err := os.OpenFile(invocation.StdoutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stdout log: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(invocation.StderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stderr log: %w", err)
	}
	defer stderr.Close()

	command := exec.CommandContext(ctx, invocation.Executable, invocation.Args...)
	command.Dir = invocation.Dir
	command.Stdin = strings.NewReader(invocation.Stdin)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("codex process: %w", err)
	}
	return nil
}

type nativeEnvelope struct {
	Findings []nativeFinding `json:"findings"`
}

type nativeFinding struct {
	Title        string
	Body         string
	Priority     int
	CodeLocation nativeCodeLocation
}

type nativeCodeLocation struct {
	AbsoluteFilePath string
	LineRange        nativeLineRange
}

type nativeLineRange struct {
	Start int
	End   int
}

type verifierEnvelope struct {
	Decisions []verifierDecision `json:"decisions"`
}

type verifierCandidate struct {
	Index   int                   `json:"index"`
	Finding quality.NativeFinding `json:"finding"`
}

type verifierDecision struct {
	Index  int    `json:"index"`
	Keep   bool   `json:"keep"`
	Reason string `json:"reason"`
}

func Run(ctx context.Context, options Options) (quality.NativeReviewResult, error) {
	if err := normalizeOptions(&options); err != nil {
		return quality.NativeReviewResult{}, err
	}
	diff, err := reviewsession.ReadRegularFile(options.Prepared.DiffPath, maxNativeOutputBytes)
	if err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("read trusted diff: %w", err)
	}
	directions := selectDirections(options.Request, diff)
	result := newResult(options, directions)
	executor := options.Executor
	if executor == nil {
		executor = ProcessExecutor{}
	}

	result.Execution.ModelCalls = 1
	if err := executor.Run(ctx, buildReviewInvocation(options, directions)); err != nil {
		markIncomplete(&result, "native review failed: "+err.Error())
		return result, nil
	}
	rawReview, err := readNativeOutput(options.Prepared.NativeReviewPath)
	if err != nil {
		markIncomplete(&result, "native review output is invalid: "+err.Error())
		return result, nil
	}
	findings, drops := adaptFindings(rawReview.Findings, options.Prepared.RepositoryDir, options.Request.ChangedFiles)
	result.Execution.AdapterDrops = drops
	if len(rawReview.Findings) > 0 && len(findings) == 0 {
		markIncomplete(&result, "native review returned candidates, but none mapped to the trusted changed-file scope")
		return result, nil
	}
	if len(findings) == 0 {
		markPass(&result, "native review reported no actionable findings")
		return result, nil
	}

	result.Execution.ModelCalls = 2
	verifierInvocation, err := buildVerifierInvocation(options, findings)
	if err != nil {
		failOpen(&result, findings, "build verifier input: "+err.Error())
		return result, nil
	}
	if err := executor.Run(ctx, verifierInvocation); err != nil {
		failOpen(&result, findings, "candidate verifier failed: "+err.Error())
		return result, nil
	}
	verifier, err := readVerifierOutput(options.Prepared.VerifierOutputPath, len(findings))
	if err != nil {
		failOpen(&result, findings, "candidate verifier output is invalid: "+err.Error())
		return result, nil
	}
	result.Execution.VerifierStatus = quality.VerifierComplete
	result.Findings = filterFindings(findings, verifier.Decisions)
	if len(result.Findings) == 0 {
		markPass(&result, "all native candidates were rejected by the candidate-only verifier")
	} else {
		markManualReview(&result)
	}
	return result, nil
}

func normalizeOptions(options *Options) error {
	if errors := quality.ValidateRequest(options.Request); len(errors) > 0 {
		return fmt.Errorf("review request is invalid: %s", strings.Join(errors, "; "))
	}
	for name, value := range map[string]string{
		"session":           options.Prepared.SessionDir,
		"repository":        options.Prepared.RepositoryDir,
		"diff":              options.Prepared.DiffPath,
		"verifier schema":   options.Prepared.VerifierSchemaPath,
		"native review log": options.Prepared.NativeReviewPath,
		"verifier output":   options.Prepared.VerifierOutputPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}
	if strings.TrimSpace(options.Goal) == "" {
		options.Goal = "Find actionable defects introduced or worsened by this change."
	}
	if len(options.Goal) > 4000 {
		return errors.New("review goal exceeds 4000 bytes")
	}
	if options.ReasoningEffort == "" {
		options.ReasoningEffort = "high"
	}
	validEffort := map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true}
	if !validEffort[options.ReasoningEffort] {
		return fmt.Errorf("unsupported reasoning effort %q", options.ReasoningEffort)
	}
	if options.EvaluationRubricVersion == "" {
		options.EvaluationRubricVersion = quality.EvaluationRubricVersion
	}
	if options.CodexBinary == "" {
		options.CodexBinary = "codex"
	}
	return nil
}

func newResult(options Options, directions []quality.ReviewDirection) quality.NativeReviewResult {
	return quality.NativeReviewResult{
		SchemaVersion:           quality.NativeResultSchemaVersion,
		EvaluationRubricVersion: options.EvaluationRubricVersion,
		Request:                 options.Request,
		ReviewGoal:              options.Goal,
		Directions:              directions,
		Findings:                []quality.NativeFinding{},
		Execution: quality.NativeExecution{
			Host: "codex", ReviewMode: "native_review", Model: options.Model,
			ReasoningEffort: options.ReasoningEffort, VerifierStatus: quality.VerifierNotNeeded,
			AdapterDrops: []quality.AdapterDrop{},
		},
		Adjudication: quality.Adjudication{
			SemanticResult: quality.ResultIncomplete,
			RolloutMode:    "report_only", CIAction: "publish_report", Reasons: []string{},
		},
	}
}

func buildReviewInvocation(options Options, directions []quality.ReviewDirection) Invocation {
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	args := []string{"exec", "--sandbox", "read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "review"}
	args = appendModelOptions(args, options)
	args = append(args,
		"--output-last-message", options.Prepared.NativeReviewPath,
		"-",
	)
	return Invocation{
		Executable: options.CodexBinary, Args: args, Dir: options.Prepared.RepositoryDir,
		Stdin: buildReviewPrompt(options, directions), OutputPath: options.Prepared.NativeReviewPath,
		StdoutPath: layout.NativeStdoutPath, StderrPath: layout.NativeStderrPath,
	}
}

func buildReviewPrompt(options Options, directions []quality.ReviewDirection) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the committed change %s..%s in the current repository.\n", options.Request.BaseCommit, options.Request.TargetCommit)
	fmt.Fprintf(&prompt, "Use `git diff --no-ext-diff --unified=6 %s %s --` as the exact review scope.\n", options.Request.BaseCommit, options.Request.TargetCommit)
	fmt.Fprintf(&prompt, "Change goal (user-provided): %s\n", strconv.Quote(options.Goal))
	prompt.WriteString("Potential risk directions (hints only, not an exhaustive checklist):\n")
	for _, direction := range directions {
		fmt.Fprintf(&prompt, "- %s\n", direction.Prompt)
	}
	prompt.WriteString("Report actionable defects outside these hints too. Only report problems introduced or worsened by this change. Do not modify files.\n")
	return prompt.String()
}

func buildVerifierInvocation(options Options, findings []quality.NativeFinding) (Invocation, error) {
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	indexed := make([]verifierCandidate, len(findings))
	for index, finding := range findings {
		indexed[index] = verifierCandidate{Index: index, Finding: finding}
	}
	candidates, err := json.Marshal(indexed)
	if err != nil {
		return Invocation{}, err
	}
	args := []string{"exec", "--sandbox", "read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral"}
	args = appendModelOptions(args, options)
	args = append(args,
		"--output-schema", options.Prepared.VerifierSchemaPath,
		"--output-last-message", options.Prepared.VerifierOutputPath,
		"-",
	)
	prompt := fmt.Sprintf(
		"Falsify only the indexed candidates below against committed change %s..%s. Each candidate has a zero-based integer index; copy its exact index into the corresponding decision and return exactly one decision per candidate. Keep a candidate unless the code proves it is not introduced or worsened, is contradicted, or is not actionable. Do not add candidates. Do not rewrite candidates. Do not merge or reprioritize them.\nCandidates:\n%s\n",
		options.Request.BaseCommit, options.Request.TargetCommit, candidates,
	)
	return Invocation{
		Executable: options.CodexBinary, Args: args, Dir: options.Prepared.RepositoryDir,
		Stdin: prompt, OutputPath: options.Prepared.VerifierOutputPath,
		StdoutPath: layout.VerifierStdoutPath, StderrPath: layout.VerifierStderrPath,
	}, nil
}

func appendModelOptions(args []string, options Options) []string {
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", options.Model)
	}
	return append(args, "--config", "model_reasoning_effort="+strconv.Quote(options.ReasoningEffort))
}

func readNativeOutput(path string) (nativeEnvelope, error) {
	raw, err := reviewsession.ReadRegularFile(path, maxNativeOutputBytes)
	if err != nil {
		return nativeEnvelope{}, err
	}
	return parseNativeReviewText(string(raw))
}

func parseNativeReviewText(raw string) (nativeEnvelope, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	first := firstNonBlankLine(lines)
	if first < 0 {
		return nativeEnvelope{}, errors.New("native review output is empty")
	}
	if strings.TrimSpace(lines[first]) == "No findings." {
		for _, line := range lines[first+1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "Review comment:" || strings.HasPrefix(trimmed, "- [P") {
				return nativeEnvelope{}, errors.New("native review contradicts its no-findings result")
			}
		}
		return nativeEnvelope{Findings: []nativeFinding{}}, nil
	}

	heading := -1
	for index := first; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "Review comment:" {
			heading = index
			break
		}
	}
	if heading < 0 {
		for _, line := range lines[first:] {
			_, recognized, err := parseNativeFindingHeader(line)
			if err != nil {
				return nativeEnvelope{}, err
			}
			if recognized {
				return nativeEnvelope{}, errors.New("native finding appears without a review comment section")
			}
		}
		return nativeEnvelope{Findings: []nativeFinding{}}, nil
	}

	findings := []nativeFinding{}
	body := []string{}
	var current *nativeFinding
	trailingAssessment := false
	flush := func() error {
		if current == nil {
			return nil
		}
		current.Body = strings.TrimSpace(strings.Join(body, "\n"))
		if current.Body == "" {
			return fmt.Errorf("native finding %d has no body", len(findings))
		}
		findings = append(findings, *current)
		current = nil
		body = nil
		return nil
	}

	for _, line := range lines[heading+1:] {
		finding, recognized, err := parseNativeFindingHeader(line)
		if err != nil {
			return nativeEnvelope{}, err
		}
		if recognized {
			if trailingAssessment {
				return nativeEnvelope{}, errors.New("native finding appears after the trailing assessment")
			}
			if err := flush(); err != nil {
				return nativeEnvelope{}, err
			}
			current = &finding
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if current == nil {
			return nativeEnvelope{}, fmt.Errorf("unexpected text in native review comment section: %q", strings.TrimSpace(line))
		}
		if line[0] == ' ' || line[0] == '\t' {
			if trailingAssessment {
				continue
			}
			body = append(body, strings.TrimSpace(line))
			continue
		}
		if len(body) == 0 {
			return nativeEnvelope{}, fmt.Errorf("native finding %d has no body", len(findings))
		}
		trailingAssessment = true
	}
	if err := flush(); err != nil {
		return nativeEnvelope{}, err
	}
	if len(findings) == 0 {
		return nativeEnvelope{}, errors.New("native review comment section contains no findings")
	}
	return nativeEnvelope{Findings: findings}, nil
}

func firstNonBlankLine(lines []string) int {
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			return index
		}
	}
	return -1
}

func parseNativeFindingHeader(line string) (nativeFinding, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "- [P") {
		return nativeFinding{}, false, nil
	}
	if len(trimmed) < 7 || trimmed[5] != ']' || trimmed[6] != ' ' || trimmed[4] < '0' || trimmed[4] > '3' {
		return nativeFinding{}, true, fmt.Errorf("malformed native finding header: %q", trimmed)
	}
	rest := strings.TrimSpace(trimmed[7:])
	delimiter := strings.LastIndex(rest, " — ")
	if delimiter < 1 {
		return nativeFinding{}, true, fmt.Errorf("native finding header has no title/location delimiter: %q", trimmed)
	}
	title := strings.TrimSpace(rest[:delimiter])
	location := strings.TrimSpace(rest[delimiter+len(" — "):])
	path, start, end, err := parseNativeLocation(location)
	if err != nil {
		return nativeFinding{}, true, fmt.Errorf("native finding location %q is invalid: %w", location, err)
	}
	if title == "" {
		return nativeFinding{}, true, errors.New("native finding title is empty")
	}
	return nativeFinding{
		Title:    title,
		Priority: int(trimmed[4] - '0'),
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: path,
			LineRange:        nativeLineRange{Start: start, End: end},
		},
	}, true, nil
}

func parseNativeLocation(raw string) (string, int, int, error) {
	separator := strings.LastIndex(raw, ":")
	if separator < 1 || separator == len(raw)-1 {
		return "", 0, 0, errors.New("expected absolute-path:start-end")
	}
	path := strings.TrimSpace(raw[:separator])
	lineRange := strings.TrimSpace(raw[separator+1:])
	startText, endText, hasEnd := strings.Cut(lineRange, "-")
	start, err := strconv.Atoi(startText)
	if err != nil || start < 1 {
		return "", 0, 0, errors.New("start line must be positive")
	}
	end := start
	if hasEnd {
		end, err = strconv.Atoi(endText)
		if err != nil || end < start {
			return "", 0, 0, errors.New("end line must be at least the start line")
		}
	}
	return path, start, end, nil
}

func readVerifierOutput(path string, count int) (verifierEnvelope, error) {
	raw, err := reviewsession.ReadRegularFile(path, maxNativeOutputBytes)
	if err != nil {
		return verifierEnvelope{}, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return verifierEnvelope{}, err
	}
	value, exists := shape["decisions"]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return verifierEnvelope{}, errors.New("decisions array is required")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		return verifierEnvelope{}, errors.New("decisions must be an array of objects")
	}
	for index, item := range items {
		if err := requireRawFields(item, fmt.Sprintf("decisions[%d]", index), "index", "keep", "reason"); err != nil {
			return verifierEnvelope{}, err
		}
	}
	verifier, err := quality.DecodeStrict[verifierEnvelope](bytes.NewReader(raw))
	if err != nil {
		return verifierEnvelope{}, err
	}
	if len(verifier.Decisions) != count {
		return verifierEnvelope{}, fmt.Errorf("decision count %d does not match candidate count %d", len(verifier.Decisions), count)
	}
	seen := make(map[int]struct{}, count)
	for _, decision := range verifier.Decisions {
		if decision.Index < 0 || decision.Index >= count {
			return verifierEnvelope{}, fmt.Errorf("decision index %d is out of range", decision.Index)
		}
		if _, exists := seen[decision.Index]; exists {
			return verifierEnvelope{}, fmt.Errorf("decision index %d is duplicated", decision.Index)
		}
		if strings.TrimSpace(decision.Reason) == "" {
			return verifierEnvelope{}, fmt.Errorf("decision index %d has no reason", decision.Index)
		}
		seen[decision.Index] = struct{}{}
	}
	return verifier, nil
}

func requireRawFields(document map[string]json.RawMessage, prefix string, fields ...string) error {
	if document == nil {
		return errors.New(prefix + " must be an object")
	}
	for _, field := range fields {
		value, exists := document[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s.%s is required", prefix, field)
		}
	}
	return nil
}

func adaptFindings(raw []nativeFinding, repository string, changedFiles []string) ([]quality.NativeFinding, []quality.AdapterDrop) {
	changed := make(map[string]struct{}, len(changedFiles))
	for _, path := range changedFiles {
		changed[filepath.ToSlash(path)] = struct{}{}
	}
	findings := make([]quality.NativeFinding, 0, len(raw))
	drops := []quality.AdapterDrop{}
	for index, candidate := range raw {
		reason := validateNativeCandidate(candidate)
		relative := ""
		if reason == "" {
			var err error
			relative, err = filepath.Rel(repository, filepath.Clean(candidate.CodeLocation.AbsoluteFilePath))
			if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				reason = "code location is outside the isolated checkout"
			} else {
				relative = filepath.ToSlash(relative)
				if _, exists := changed[relative]; !exists {
					reason = "code location is not in a changed file"
				}
			}
		}
		if reason != "" {
			drops = append(drops, quality.AdapterDrop{Index: index, Reason: reason})
			continue
		}
		findings = append(findings, quality.NativeFinding{
			Title: strings.TrimSpace(candidate.Title), Body: strings.TrimSpace(candidate.Body),
			Priority: candidate.Priority,
			CodeLocation: quality.NativeCodeLocation{
				Path: relative, StartLine: candidate.CodeLocation.LineRange.Start, EndLine: candidate.CodeLocation.LineRange.End,
			},
		})
	}
	return findings, drops
}

func validateNativeCandidate(candidate nativeFinding) string {
	if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" {
		return "title and body are required"
	}
	if candidate.Priority < 0 || candidate.Priority > 3 {
		return "priority is outside 0-3"
	}
	if !filepath.IsAbs(candidate.CodeLocation.AbsoluteFilePath) {
		return "code location must be absolute"
	}
	if candidate.CodeLocation.LineRange.Start < 1 || candidate.CodeLocation.LineRange.End < candidate.CodeLocation.LineRange.Start {
		return "line range is invalid"
	}
	return ""
}

func filterFindings(findings []quality.NativeFinding, decisions []verifierDecision) []quality.NativeFinding {
	keep := make(map[int]bool, len(decisions))
	for _, decision := range decisions {
		keep[decision.Index] = decision.Keep
	}
	result := make([]quality.NativeFinding, 0, len(findings))
	for index, finding := range findings {
		if keep[index] {
			result = append(result, finding)
		}
	}
	return result
}

func markPass(result *quality.NativeReviewResult, reason string) {
	result.Adjudication.SemanticResult = quality.ResultPass
	result.Adjudication.Reasons = []string{reason}
}

func markManualReview(result *quality.NativeReviewResult) {
	result.Adjudication.SemanticResult = quality.ResultManualReview
	result.Adjudication.Reasons = []string{fmt.Sprintf("%d native finding(s) require manual review", len(result.Findings))}
}

func markIncomplete(result *quality.NativeReviewResult, reason string) {
	result.Findings = []quality.NativeFinding{}
	result.Adjudication.SemanticResult = quality.ResultIncomplete
	result.Adjudication.Reasons = []string{reason}
}

func failOpen(result *quality.NativeReviewResult, findings []quality.NativeFinding, note string) {
	result.Findings = findings
	result.Execution.VerifierStatus = quality.VerifierFailedOpen
	result.Execution.VerifierNote = note
	markManualReview(result)
}

func Publish(prepared reviewsession.Prepared, result quality.NativeReviewResult) error {
	if problems := quality.ValidateNativeResult(result); len(problems) > 0 {
		return fmt.Errorf("native review result is invalid: %s", strings.Join(problems, "; "))
	}
	if err := writeExclusiveJSON(prepared.ResultPath, result); err != nil {
		return fmt.Errorf("write native review result: %w", err)
	}
	if err := writeExclusiveFile(prepared.MarkdownPath, []byte(quality.RenderNativeMarkdown(result))); err != nil {
		return fmt.Errorf("write native review markdown: %w", err)
	}
	return nil
}

func writeExclusiveFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeExclusiveJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := quality.EncodeJSON(file, value); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
