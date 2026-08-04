package codexreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type nativeRunOptions struct {
	Session         reviewsession.NativeSession
	Goal            string
	Model           string
	ReasoningEffort string
	CodexBinary     string
	LeaseFile       *os.File
}

func runNativeSession(ctx context.Context, options nativeRunOptions) (quality.NativeOutcome, error) {
	if err := normalizeRunOptions(&options); err != nil {
		return quality.NativeOutcome{}, err
	}
	captured, err := captureNativeEvidence(ctx, options)
	if err != nil {
		return quality.NativeOutcome{}, err
	}
	return quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request:         options.Session.Request(),
		ReviewGoal:      options.Goal,
		Model:           options.Model,
		ReasoningEffort: options.ReasoningEffort,
	}, captured.finalMessage, captured.processErr)
}

func normalizeRunOptions(options *nativeRunOptions) error {
	request := options.Session.Request()
	if problems := quality.ValidateRequest(request); len(problems) > 0 {
		return fmt.Errorf("review request is invalid: %s", strings.Join(problems, "; "))
	}
	artifacts := options.Session.Artifacts()
	for name, value := range map[string]string{
		"session":           options.Session.Directory(),
		"repository":        options.Session.RepositoryDirectory(),
		"native review log": artifacts.FinalMessagePath(),
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
	if options.CodexBinary == "" {
		options.CodexBinary = "codex"
	}
	return nil
}

func buildReviewInvocation(options nativeRunOptions) reviewInvocation {
	artifacts := options.Session.Artifacts()
	args := []string{
		"exec", "--sandbox", "workspace-write",
		"--config", "sandbox_workspace_write.network_access=true",
	}
	args = appendModelOptions(args, options)
	args = append(args,
		"--json",
		"--output-last-message", artifacts.FinalMessagePath(),
		"-",
	)
	invocation := reviewInvocation{
		executable: options.CodexBinary,
		args:       args,
		directory:  options.Session.RepositoryDirectory(),
		stdin:      buildReviewPrompt(options),
		paths:      capturePathsFromSession(options.Session),
	}
	if options.LeaseFile != nil {
		invocation.extraFiles = []*os.File{options.LeaseFile}
	}
	return invocation
}

func buildReviewPrompt(options nativeRunOptions) string {
	request := options.Session.Request()
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the changes introduced by %s relative to %s for actionable defects.\n", request.TargetCommit, request.BaseCommit)
	if options.Goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(options.Goal))
	}
	return prompt.String()
}

func appendModelOptions(args []string, options nativeRunOptions) []string {
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", options.Model)
	}
	return append(args, "--config", "model_reasoning_effort="+strconv.Quote(options.ReasoningEffort))
}

func publishNativeOutcome(session reviewsession.NativeSession, outcome quality.NativeOutcome) error {
	artifacts := session.Artifacts()
	if err := writeExclusiveEncoded(artifacts.ResultPath(), outcome.EncodeJSON); err != nil {
		return fmt.Errorf("write native review result: %w", err)
	}
	if err := writeExclusiveFile(artifacts.MarkdownPath(), []byte(outcome.Markdown())); err != nil {
		return fmt.Errorf("write native review markdown: %w", err)
	}
	return nil
}

func writeExclusiveFile(path string, contents []byte) error {
	return writeExclusiveEncoded(path, func(writer io.Writer) error {
		_, err := writer.Write(contents)
		return err
	})
}

func writeExclusiveJSON(path string, value any) error {
	return writeExclusiveEncoded(path, func(writer io.Writer) error {
		return quality.EncodeJSON(writer, value)
	})
}

func writeExclusiveEncoded(path string, encode func(io.Writer) error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := encode(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
