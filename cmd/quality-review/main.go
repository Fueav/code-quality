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
	"github.com/Fueav/code-quality/internal/intake"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "quality-review: invoke the code-quality Skill from an active Claude Code or Codex session")
		fmt.Fprintln(stderr, "quality-review: deterministic commands: prepare, finalize, adjudicate, validate, render")
		return 2
	}
	policy, err := loadPolicy()
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: load embedded policy: %v\n", err)
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
	case "prepare":
		return runPrepare(args[1:], stdout, stderr)
	case "finalize":
		return runFinalize(args[1:], policy, stdout, stderr)
	case "adjudicate":
		return runAdjudicate(args[1:], policy, stdout, stderr)
	case "validate":
		return runValidate(args[1:], policy, stdout, stderr)
	case "render":
		return runRender(args[1:], policy, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "quality-review: unknown command %q\n", args[0])
		return 2
	}
}

func runPrepare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repository := flags.String("repo", ".", "Git repository path")
	base := flags.String("base", "", "base commit")
	target := flags.String("target", "", "target commit")
	reason := flags.String("diff-reason", "", "diff selection reason")
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
	verifierUnavailable := flags.String("verifier-unavailable", "", "reason a verifier Agent could not run")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sessionDir) == "" {
		fmt.Fprintln(stderr, "usage: quality-review finalize --session <directory> [--verifier-unavailable <reason>]")
		return 2
	}
	result, err := reviewsession.Finalize(reviewsession.FinalizeOptions{
		SessionDir:          *sessionDir,
		VerifierUnavailable: *verifierUnavailable,
	}, policy)
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
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: quality-review adjudicate <request.json> <model-review.json>")
		return 2
	}
	request, requestErr := decodeFile[quality.ReviewRequest](args[0])
	review, reviewErr := decodeModelReviewFile(args[1])
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
	review.Execution = quality.Execution{AgentCount: 1}
	if validationErrors := quality.ValidateMainReview(review, policy); len(validationErrors) > 0 {
		return encodeResult(stdout, stderr, quality.IncompleteResult(request, policy, "invalid main review: "+strings.Join(validationErrors, "; ")))
	}
	return encodeResult(stdout, stderr, quality.Adjudicate(request, review, policy))
}

func runValidate(args []string, policy quality.PolicyManifest, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: quality-review validate <review-result.json>")
		return 2
	}
	result, err := decodeFile[quality.ReviewResult](args[0])
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
	result, err := decodeFile[quality.ReviewResult](args[0])
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

func decodeFile[T any](path string) (T, error) {
	file, err := os.Open(path)
	if err != nil {
		var zero T
		return zero, err
	}
	defer file.Close()
	return quality.DecodeStrict[T](file)
}
