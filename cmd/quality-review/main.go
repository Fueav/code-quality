package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/internal/intake"
	"github.com/Fueav/code-quality/quality"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runPrepare(nil, stdout, stderr)
	}
	policy, err := loadPolicy()
	if err != nil {
		fmt.Fprintf(stderr, "quality-review: load embedded policy: %v\n", err)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "quality-review %s\n", version)
		return 0
	case "prepare":
		return runPrepare(args[1:], stdout, stderr)
	case "adjudicate":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "usage: quality-review adjudicate <request.json> <model-review.json>")
			return 2
		}
		request, requestErr := decodeFile[quality.ReviewRequest](args[1])
		review, reviewErr := decodeFile[quality.ModelReview](args[2])
		if requestErr != nil || reviewErr != nil {
			reasons := []string{}
			if requestErr != nil {
				reasons = append(reasons, "invalid review request: "+requestErr.Error())
			}
			if reviewErr != nil {
				reasons = append(reasons, "invalid model review: "+reviewErr.Error())
			}
			if err := quality.EncodeJSON(stdout, quality.IncompleteResult(request, policy, reasons...)); err != nil {
				fmt.Fprintf(stderr, "quality-review: encode incomplete result: %v\n", err)
				return 2
			}
			return 0
		}
		if err := quality.EncodeJSON(stdout, quality.Adjudicate(request, review, policy)); err != nil {
			fmt.Fprintf(stderr, "quality-review: encode result: %v\n", err)
			return 2
		}
		return 0
	case "validate":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: quality-review validate <review-result.json>")
			return 2
		}
		result, err := decodeFile[quality.ReviewResult](args[1])
		if err != nil {
			fmt.Fprintf(stderr, "quality-review: invalid result: %v\n", err)
			return 1
		}
		if errors := quality.ValidateResult(result, policy); len(errors) > 0 {
			for _, message := range errors {
				fmt.Fprintf(stderr, "quality-review: %s\n", message)
			}
			return 1
		}
		fmt.Fprintln(stdout, "review result is valid")
		return 0
	case "render":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: quality-review render <review-result.json>")
			return 2
		}
		result, err := decodeFile[quality.ReviewResult](args[1])
		if err != nil {
			fmt.Fprintf(stderr, "quality-review: invalid result: %v\n", err)
			return 1
		}
		if errors := quality.ValidateResult(result, policy); len(errors) > 0 {
			for _, message := range errors {
				fmt.Fprintf(stderr, "quality-review: %s\n", message)
			}
			return 1
		}
		fmt.Fprint(stdout, quality.RenderMarkdown(result))
		return 0
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
	if result.DirtyWorktree {
		fmt.Fprintln(stderr, "quality-review: working tree changes are not included; review covers committed base and target only")
	}
	if err := quality.EncodeJSON(stdout, result.Request); err != nil {
		fmt.Fprintf(stderr, "quality-review: encode request: %v\n", err)
		return 2
	}
	return 0
}

func loadPolicy() (quality.PolicyManifest, error) {
	raw, err := bundle.PolicyManifest()
	if err != nil {
		return quality.PolicyManifest{}, err
	}
	return quality.DecodeStrict[quality.PolicyManifest](bytesReader(raw))
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

type byteReader struct {
	value []byte
}

func bytesReader(value []byte) *byteReader {
	return &byteReader{value: value}
}

func (reader *byteReader) Read(target []byte) (int, error) {
	if len(reader.value) == 0 {
		return 0, io.EOF
	}
	count := copy(target, reader.value)
	reader.value = reader.value[count:]
	return count, nil
}
