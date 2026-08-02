package codexreview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const (
	maxNativeOutputBytes         = int64(10 << 20)
	DiscoveryChildMarkerName     = ".code-quality-native-discovery-child-v1"
	discoveryChildMarkerContents = "code-quality native discovery child v1\n"
)

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
	markerPath, err := installDiscoveryChildMarker(invocation.Dir)
	if err != nil {
		return fmt.Errorf("install discovery child marker: %w", err)
	}
	markerInstalled := true
	defer func() {
		if markerInstalled {
			_ = os.Remove(markerPath)
		}
	}()
	runErr := command.Run()
	if err := os.Remove(markerPath); err != nil {
		return fmt.Errorf("remove discovery child marker: %w", err)
	}
	markerInstalled = false
	if runErr != nil {
		return fmt.Errorf("codex process: %w", runErr)
	}
	return nil
}

func installDiscoveryChildMarker(repositoryDir string) (string, error) {
	markerPath := discoveryChildMarkerPath(repositoryDir)
	marker, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return "", err
	}
	installed := false
	defer func() {
		_ = marker.Close()
		if !installed {
			_ = os.Remove(markerPath)
		}
	}()
	if _, err := marker.WriteString(discoveryChildMarkerContents); err != nil {
		return "", err
	}
	if err := marker.Close(); err != nil {
		return "", err
	}
	installed = true
	return markerPath, nil
}

func IsDiscoveryChildRepository(repositoryDir string) (bool, error) {
	return isDiscoveryChildMarker(discoveryChildMarkerPath(repositoryDir))
}

func IsDiscoveryChildWorkingDirectory(workingDir string) (bool, error) {
	current, err := filepath.Abs(workingDir)
	if err != nil {
		return false, err
	}
	for {
		nested, err := isDiscoveryChildMarker(filepath.Join(current, DiscoveryChildMarkerName))
		if err != nil || nested {
			return nested, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func isDiscoveryChildMarker(path string) (bool, error) {
	raw, err := reviewsession.ReadRegularFile(path, int64(len(discoveryChildMarkerContents)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(raw) == discoveryChildMarkerContents, nil
}

func discoveryChildMarkerPath(repositoryDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(repositoryDir)), DiscoveryChildMarkerName)
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

type NativeRunMetrics struct {
	SchemaVersion    int    `json:"schema_version"`
	DurationMS       int64  `json:"duration_ms"`
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	UsageAvailable   bool   `json:"usage_available"`
	UsageError       string `json:"usage_error,omitempty"`
	ChangedFileCount int    `json:"changed_file_count"`
	TrustedDiffBytes int64  `json:"trusted_diff_bytes"`
}

type codexEvent struct {
	Type  string      `json:"type"`
	Usage *codexUsage `json:"usage,omitempty"`
}

type codexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func Run(ctx context.Context, options Options) (quality.NativeReviewResult, error) {
	if err := normalizeOptions(&options); err != nil {
		return quality.NativeReviewResult{}, err
	}
	trustedDiff, err := reviewsession.ReadRegularFile(options.Prepared.DiffPath, maxNativeOutputBytes)
	if err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("read trusted diff: %w", err)
	}
	result := newResult(options)
	executor := options.Executor
	if executor == nil {
		executor = ProcessExecutor{}
	}

	result.Execution.ModelCalls = 1
	started := time.Now()
	runErr := executor.Run(ctx, buildReviewInvocation(options))
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	frozen, err := freezeNativeArtifacts(layout)
	if err != nil {
		return quality.NativeReviewResult{}, err
	}
	metrics := collectRunMetricsFromJSONL(options, time.Since(started), int64(len(trustedDiff)), frozen.Stdout)
	if err := writeExclusiveJSON(reviewsession.NewLayout(options.Prepared.SessionDir).NativeMetricsPath, metrics); err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("write native run metrics: %w", err)
	}
	if runErr != nil {
		markIncomplete(&result, "native review failed: "+runErr.Error())
		return result, nil
	}
	if len(frozen.FinalMessage) == 0 {
		markIncomplete(&result, "native review output is missing or empty")
		return result, nil
	}
	rawReview, err := parseNativeReviewText(string(frozen.FinalMessage))
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
	result.Findings = findings
	markManualReview(&result)
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
		"native review log": options.Prepared.NativeReviewPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}
	options.Goal = strings.TrimSpace(options.Goal)
	if len(options.Goal) > 4000 {
		return errors.New("review goal exceeds 4000 bytes")
	}
	if options.Model == "" {
		options.Model = "gpt-5.6-sol"
	}
	if options.ReasoningEffort == "" {
		options.ReasoningEffort = "max"
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

func newResult(options Options) quality.NativeReviewResult {
	return quality.NativeReviewResult{
		SchemaVersion:           quality.NativeResultSchemaVersion,
		EvaluationRubricVersion: options.EvaluationRubricVersion,
		Request:                 options.Request,
		ReviewGoal:              options.Goal,
		Findings:                []quality.NativeFinding{},
		Execution: quality.NativeExecution{
			Host: "codex", ReviewMode: "native_review", Model: options.Model,
			ReasoningEffort: options.ReasoningEffort, AdapterDrops: []quality.AdapterDrop{},
		},
		Adjudication: quality.Adjudication{
			SemanticResult: quality.ResultIncomplete,
			RolloutMode:    "report_only", CIAction: "publish_report", Reasons: []string{},
		},
	}
}

func buildReviewInvocation(options Options) Invocation {
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	args := []string{
		"exec", "--sandbox", "workspace-write",
		"--config", "sandbox_workspace_write.network_access=true",
	}
	args = appendModelOptions(args, options)
	args = append(args,
		"--json",
		"--output-last-message", options.Prepared.NativeReviewPath,
		"-",
	)
	return Invocation{
		Executable: options.CodexBinary, Args: args, Dir: options.Prepared.RepositoryDir,
		Stdin: buildReviewPrompt(options), OutputPath: options.Prepared.NativeReviewPath,
		StdoutPath: layout.NativeStdoutPath, StderrPath: layout.NativeStderrPath,
	}
}

func collectRunMetrics(options Options, duration time.Duration, trustedDiffBytes int64) NativeRunMetrics {
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	raw, err := reviewsession.ReadRegularFile(layout.NativeStdoutPath, maxNativeOutputBytes)
	if err != nil {
		return unavailableRunMetrics(options, duration, trustedDiffBytes, err)
	}
	return collectRunMetricsFromJSONL(options, duration, trustedDiffBytes, raw)
}

func collectRunMetricsFromJSONL(options Options, duration time.Duration, trustedDiffBytes int64, raw []byte) NativeRunMetrics {
	metrics := NativeRunMetrics{
		SchemaVersion: 1, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(options.Request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
	}
	inputTokens, outputTokens, err := decodeCodexUsage(raw)
	if err != nil {
		metrics.UsageError = err.Error()
		return metrics
	}
	metrics.InputTokens = inputTokens
	metrics.OutputTokens = outputTokens
	metrics.UsageAvailable = inputTokens != nil && outputTokens != nil
	return metrics
}

func unavailableRunMetrics(options Options, duration time.Duration, trustedDiffBytes int64, err error) NativeRunMetrics {
	return NativeRunMetrics{
		SchemaVersion: 1, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(options.Request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
		UsageError: err.Error(),
	}
}

func readCodexUsage(path string) (*int64, *int64, error) {
	raw, err := reviewsession.ReadRegularFile(path, maxNativeOutputBytes)
	if err != nil {
		return nil, nil, err
	}
	return decodeCodexUsage(raw)
}

func decodeCodexUsage(raw []byte) (*int64, *int64, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 64<<10), int(maxNativeOutputBytes))
	var latest *codexUsage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, nil, fmt.Errorf("decode Codex JSONL event: %w", err)
		}
		if event.Type == "turn.completed" && event.Usage != nil {
			if event.Usage.InputTokens < 0 || event.Usage.OutputTokens < 0 {
				return nil, nil, errors.New("Codex usage tokens must be non-negative")
			}
			copy := *event.Usage
			latest = &copy
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan Codex JSONL events: %w", err)
	}
	if latest == nil {
		return nil, nil, errors.New("Codex JSONL has no turn.completed usage event")
	}
	if latest.InputTokens == 0 && latest.OutputTokens == 0 {
		return nil, nil, errors.New("Codex JSONL reported zero token usage; counters are unavailable")
	}
	inputTokens := latest.InputTokens
	outputTokens := latest.OutputTokens
	return &inputTokens, &outputTokens, nil
}

func buildReviewPrompt(options Options) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the changes introduced by %s relative to %s for actionable defects.\n", options.Request.TargetCommit, options.Request.BaseCommit)
	if options.Goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(options.Goal))
	}
	return prompt.String()
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
	noFindingsLine := -1
	if isExplicitNoFindings(lines[first]) {
		noFindingsLine = first
	} else {
		for index := first; index < len(lines); index++ {
			if isFindingsContainerHeading(lines[index]) {
				noFindingsLine = nextNonBlankLine(lines, index+1)
				break
			}
		}
	}
	if noFindingsLine >= 0 && isExplicitNoFindings(lines[noFindingsLine]) {
		for _, line := range lines[first:noFindingsLine] {
			if containsPriorityMarker(line) {
				return nativeEnvelope{}, errors.New("native review contradicts its no-findings result")
			}
		}
		for _, line := range lines[noFindingsLine+1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if isNativeReviewHeading(line) || containsPriorityMarker(line) {
				return nativeEnvelope{}, errors.New("native review contradicts its no-findings result")
			}
			return nativeEnvelope{}, fmt.Errorf("unexpected text after native no-findings result: %q", strings.TrimSpace(line))
		}
		return nativeEnvelope{Findings: []nativeFinding{}}, nil
	}

	heading := -1
	for index := first; index < len(lines); index++ {
		if isNativeReviewHeading(lines[index]) {
			heading = index
			break
		}
	}
	if heading < 0 {
		return parseAgentReviewText(lines, first)
	}
	nativeResult, nativeErr := parseNativeReviewSection(lines, heading)
	if nativeErr == nil {
		return nativeResult, nil
	}
	agentResult, agentErr := parseAgentReviewText(lines, first)
	if agentErr == nil {
		return agentResult, nil
	}
	return nativeEnvelope{}, fmt.Errorf("unrecognized review findings: native format: %v; agent format: %v", nativeErr, agentErr)
}

func parseNativeReviewSection(lines []string, heading int) (nativeEnvelope, error) {
	findings := []nativeFinding{}
	body := []string{}
	var current *nativeFinding
	findingIndent := -1
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
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent, topLevel := commonMarkIndent(line)
		finding := nativeFinding{}
		recognized := false
		var err error
		if topLevel && (findingIndent < 0 || indent <= findingIndent) {
			finding, recognized, err = parseNativeFindingHeader(line)
		}
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
			findingIndent = indent
			continue
		}
		if current != nil && (!topLevel || indent > findingIndent) {
			if !trailingAssessment {
				body = append(body, trimmed)
			}
			continue
		}
		if isExplicitNoFindings(line) {
			return nativeEnvelope{}, errors.New("native findings contradict a trailing no-findings result")
		}
		if current == nil {
			return nativeEnvelope{}, fmt.Errorf("unexpected text in native review comment section: %q", strings.TrimSpace(line))
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

var (
	agentListPrefixPattern     = regexp.MustCompile(`^(?:[-*+]|[0-9]+[.)])[ \t]+`)
	markdownDestinationPattern = regexp.MustCompile(`^(.+):([0-9]+)(?:-([0-9]+))?$`)
)

type markdownLocationMatch struct {
	fullStart int
	fullEnd   int
	path      string
	start     string
	end       string
}

func parseAgentReviewText(lines []string, first int) (nativeEnvelope, error) {
	firstCandidate := -1
	findingIndent := -1
	for index := first; index < len(lines); index++ {
		indent, topLevel := commonMarkIndent(lines[index])
		if topLevel && containsPriorityMarker(lines[index]) && agentListPrefixPattern.MatchString(strings.TrimSpace(lines[index])) {
			firstCandidate = index
			findingIndent = indent
			break
		}
	}
	if firstCandidate < 0 {
		return nativeEnvelope{}, errors.New("native review output has no explicit no-findings result or recognized findings")
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
			return fmt.Errorf("agent finding %d has no body", len(findings))
		}
		findings = append(findings, *current)
		current = nil
		body = nil
		return nil
	}

	for _, line := range lines[firstCandidate:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent, topLevel := commonMarkIndent(line)
		finding := nativeFinding{}
		initialBody := ""
		recognized := false
		var err error
		if topLevel && indent <= findingIndent {
			finding, initialBody, recognized, err = parseAgentFindingHeader(line)
		}
		if err != nil {
			return nativeEnvelope{}, err
		}
		if recognized {
			if trailingAssessment {
				return nativeEnvelope{}, errors.New("agent finding appears after trailing review text")
			}
			if err := flush(); err != nil {
				return nativeEnvelope{}, err
			}
			current = &finding
			findingIndent = indent
			if initialBody != "" {
				body = append(body, initialBody)
			}
			continue
		}
		if current != nil && (!topLevel || indent > findingIndent) {
			if !trailingAssessment {
				body = append(body, trimmed)
			}
			continue
		}
		if isExplicitNoFindings(trimmed) {
			return nativeEnvelope{}, errors.New("agent findings contradict a trailing no-findings result")
		}
		if current != nil {
			if containsPriorityMarker(line) {
				return nativeEnvelope{}, fmt.Errorf("unrecognized agent finding header: %q", trimmed)
			}
			if len(body) == 0 {
				return nativeEnvelope{}, fmt.Errorf("agent finding %d has no body", len(findings))
			}
			trailingAssessment = true
			continue
		}
		if containsPriorityMarker(line) {
			return nativeEnvelope{}, fmt.Errorf("unrecognized agent finding header: %q", trimmed)
		}
	}
	if err := flush(); err != nil {
		return nativeEnvelope{}, err
	}
	if len(findings) == 0 {
		return nativeEnvelope{}, errors.New("agent findings introduction contains no findings")
	}
	return nativeEnvelope{Findings: findings}, nil
}

func parseAgentFindingHeader(line string) (nativeFinding, string, bool, error) {
	trimmed := strings.TrimSpace(line)
	prefix := agentListPrefixPattern.FindString(trimmed)
	if prefix == "" {
		return nativeFinding{}, "", false, nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	bold := strings.HasPrefix(rest, "**")
	if bold {
		rest = strings.TrimPrefix(rest, "**")
	}
	if len(rest) < 5 || rest[0] != '[' || rest[1] != 'P' || rest[3] != ']' || rest[2] < '0' || rest[2] > '3' {
		return nativeFinding{}, "", false, nil
	}
	priority := int(rest[2] - '0')
	rest = strings.TrimSpace(rest[4:])
	location, found := findMarkdownLocation(rest)
	if !found {
		return nativeFinding{}, "", true, errors.New("agent finding has no Markdown code location")
	}
	path := strings.TrimSpace(location.path)
	start, err := strconv.Atoi(location.start)
	if err != nil || start < 1 {
		return nativeFinding{}, "", true, errors.New("agent finding start line must be positive")
	}
	end := start
	if location.end != "" {
		end, err = strconv.Atoi(location.end)
		if err != nil || end < start {
			return nativeFinding{}, "", true, errors.New("agent finding end line is invalid")
		}
	}

	beforeLocation := strings.TrimSpace(rest[:location.fullStart])
	afterLocation := strings.TrimSpace(rest[location.fullEnd:])
	title := ""
	initialBody := ""
	if bold {
		closing := strings.Index(beforeLocation, "**")
		if closing < 1 {
			return nativeFinding{}, "", true, errors.New("agent finding has an unclosed bold title")
		}
		title = strings.TrimSpace(beforeLocation[:closing])
		initialBody = cleanAgentBody(beforeLocation[closing+2:] + " " + afterLocation)
	} else {
		title = strings.TrimSpace(strings.TrimSuffix(beforeLocation, "—"))
		initialBody = cleanAgentBody(afterLocation)
	}
	if title == "" {
		return nativeFinding{}, "", true, errors.New("agent finding title is empty")
	}
	return nativeFinding{
		Title: title, Priority: priority,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: path,
			LineRange:        nativeLineRange{Start: start, End: end},
		},
	}, initialBody, true, nil
}

func findMarkdownLocation(raw string) (markdownLocationMatch, bool) {
	searchStart := 0
	for searchStart < len(raw) {
		labelEndOffset := strings.Index(raw[searchStart:], "](")
		if labelEndOffset < 0 {
			break
		}
		labelEnd := searchStart + labelEndOffset
		labelStart := strings.LastIndex(raw[:labelEnd], "[")
		if labelStart < 0 {
			searchStart = labelEnd + 2
			continue
		}
		destination, fullEnd, ok := readMarkdownDestination(raw, labelEnd+2)
		if ok {
			match := markdownDestinationPattern.FindStringSubmatch(destination)
			if match != nil {
				return markdownLocationMatch{
					fullStart: labelStart,
					fullEnd:   fullEnd,
					path:      match[1],
					start:     match[2],
					end:       match[3],
				}, true
			}
		}
		searchStart = labelEnd + 2
	}
	return markdownLocationMatch{}, false
}

func readMarkdownDestination(raw string, start int) (string, int, bool) {
	if start >= len(raw) {
		return "", 0, false
	}
	if raw[start] == '<' {
		endOffset := strings.Index(raw[start+1:], ">)")
		if endOffset < 0 {
			return "", 0, false
		}
		end := start + 1 + endOffset
		destination := raw[start+1 : end]
		if destination == "" || strings.ContainsAny(destination, "\r\n>") {
			return "", 0, false
		}
		return destination, end + 2, true
	}
	depth := 1
	escaped := false
	for index := start; index < len(raw); index++ {
		character := raw[index]
		if character == '\n' || character == '\r' {
			return "", 0, false
		}
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				if index == start {
					return "", 0, false
				}
				return raw[start:index], index + 1, true
			}
		}
	}
	return "", 0, false
}

func cleanAgentBody(raw string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "—. "))
}

func isExplicitNoFindings(line string) bool {
	if !isTopLevelMarkdownLine(line) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "no findings.", "no actionable findings.", "no actionable defects found.":
		return true
	default:
		return false
	}
}

func containsPriorityMarker(line string) bool {
	for priority := 0; priority <= 3; priority++ {
		if strings.Contains(line, fmt.Sprintf("[P%d]", priority)) {
			return true
		}
	}
	return false
}

func isNativeReviewHeading(line string) bool {
	if !isTopLevelMarkdownLine(line) {
		return false
	}
	switch strings.TrimSpace(line) {
	case "Review comment:", "Review comments:", "Full review comment:", "Full review comments:":
		return true
	default:
		return false
	}
}

func isFindingsContainerHeading(line string) bool {
	if !isTopLevelMarkdownLine(line) {
		return false
	}
	if isNativeReviewHeading(line) {
		return true
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasSuffix(trimmed, ":") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
	}
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes > 0 {
		if hashes > 6 || hashes == len(trimmed) || (trimmed[hashes] != ' ' && trimmed[hashes] != '\t') {
			return false
		}
		trimmed = strings.TrimSpace(trimmed[hashes:])
	}
	return strings.EqualFold(trimmed, "findings") || strings.EqualFold(trimmed, "review findings")
}

func firstNonBlankLine(lines []string) int {
	return nextNonBlankLine(lines, 0)
}

func nextNonBlankLine(lines []string, start int) int {
	for index := start; index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) != "" {
			return index
		}
	}
	return -1
}

func commonMarkIndent(line string) (int, bool) {
	indent := 0
	for indent < len(line) {
		switch line[indent] {
		case ' ':
			indent++
		case '\t':
			return indent, false
		default:
			return indent, indent <= 3
		}
	}
	return indent, indent <= 3
}

func isTopLevelMarkdownLine(line string) bool {
	_, topLevel := commonMarkIndent(line)
	return topLevel
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

func adaptFindings(raw []nativeFinding, repository string, changedFiles []string) ([]quality.NativeFinding, []quality.AdapterDrop) {
	changed := make(map[string]struct{}, len(changedFiles))
	for _, path := range changedFiles {
		changed[filepath.ToSlash(path)] = struct{}{}
	}
	findings := make([]quality.NativeFinding, 0, len(raw))
	drops := []quality.AdapterDrop{}
	logicalRepository := filepath.Clean(repository)
	canonicalRepository, repositoryErr := canonicalPath(logicalRepository)
	for index, candidate := range raw {
		reason := validateNativeCandidate(candidate)
		relative := ""
		if reason == "" {
			if repositoryErr != nil {
				reason = "isolated checkout path cannot be canonicalized"
			}
		}
		if reason == "" {
			canonicalCandidate, err := canonicalPath(candidate.CodeLocation.AbsoluteFilePath)
			if err != nil {
				reason = "code location cannot be canonicalized"
			} else {
				canonicalRelative, inside := repositoryRelativePath(canonicalRepository, canonicalCandidate)
				if !inside {
					reason = "code location is outside the isolated checkout"
				} else {
					logicalCandidate := filepath.Clean(candidate.CodeLocation.AbsoluteFilePath)
					logicalRelative, logicalInside := repositoryRelativePath(logicalRepository, logicalCandidate)
					if logicalInside {
						if _, exists := changed[logicalRelative]; exists {
							relative = logicalRelative
						} else {
							relative = canonicalRelative
						}
					} else {
						relative = canonicalRelative
					}
					if _, exists := changed[relative]; !exists {
						reason = "code location is not in a changed file"
					}
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

func repositoryRelativePath(repository, candidate string) (string, bool) {
	relative, err := filepath.Rel(repository, candidate)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func canonicalPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", errors.New("path must be absolute")
	}
	existing := clean
	suffix := []string{}
	seenSymlinks := map[string]struct{}{}
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		info, lstatErr := os.Lstat(existing)
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			if _, seen := seenSymlinks[existing]; seen {
				return "", errors.New("symlink cycle")
			}
			seenSymlinks[existing] = struct{}{}
			target, err := os.Readlink(existing)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(existing), target)
			}
			existing = filepath.Clean(target)
			continue
		}
		if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
			return "", lstatErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
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
	if containsParentTraversal(candidate.CodeLocation.AbsoluteFilePath) {
		return "code location contains parent traversal"
	}
	if candidate.CodeLocation.LineRange.Start < 1 || candidate.CodeLocation.LineRange.End < candidate.CodeLocation.LineRange.Start {
		return "line range is invalid"
	}
	return ""
}

func containsParentTraversal(path string) bool {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return true
		}
	}
	return false
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
