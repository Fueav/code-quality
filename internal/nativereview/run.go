package nativereview

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
	Session          reviewsession.NativeSession
	Goal             string
	Model            string
	ReasoningEffort  string
	ExecutionProfile string
	Provider         Provider
	LeaseFile        *os.File
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
		Request:          options.Session.Request(),
		ReviewGoal:       options.Goal,
		Host:             options.Provider.Host(),
		ExecutionProfile: options.ExecutionProfile,
		Model:            options.Model,
		ReasoningEffort:  options.ReasoningEffort,
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
	if options.Provider == nil {
		options.Provider = NewCodexProvider("")
	}
	if err := validateProvider(options.Provider); err != nil {
		return err
	}
	if strings.TrimSpace(options.Model) == "" {
		options.Model = options.Provider.defaultModel()
	}
	if strings.TrimSpace(options.ReasoningEffort) == "" {
		options.ReasoningEffort = options.Provider.defaultReasoningEffort()
	}
	if strings.TrimSpace(options.ExecutionProfile) == "" {
		options.ExecutionProfile = quality.ExecutionProfilePersonal
	}
	if options.ExecutionProfile != quality.ExecutionProfilePersonal && options.ExecutionProfile != quality.ExecutionProfileProductionCI {
		return fmt.Errorf("unsupported execution profile %q", options.ExecutionProfile)
	}
	return options.Provider.validateReasoningEffort(options.ReasoningEffort)
}

func buildReviewInvocation(options nativeRunOptions) reviewInvocation {
	return options.Provider.buildInvocation(providerInvocationOptions{
		Session: options.Session, Goal: options.Goal, Model: options.Model,
		ReasoningEffort: options.ReasoningEffort, ExecutionProfile: options.ExecutionProfile,
		LeaseFile: options.LeaseFile,
	})
}

func buildReviewPrompt(request quality.ReviewRequest, goal string, reportOnlyBoundary bool) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the changes introduced by %s relative to %s for actionable defects.\n", request.TargetCommit, request.BaseCommit)
	if goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(goal))
	}
	if reportOnlyBoundary {
		prompt.WriteString("Report findings only. Do not modify files, commit, push, deploy, or change external state.\n")
	}
	return prompt.String()
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
