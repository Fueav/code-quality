package nativereview

import (
	"fmt"
	"io"
	"os"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

// Provider is the deliberately small boundary between the evidence lifecycle
// and one native review CLI. Implementations may describe the CLI protocol,
// but they do not add review methodology or orchestration.
type Provider interface {
	Host() string
	defaultModel() string
	defaultReasoningEffort() string
	validateReasoningEffort(string) error
	buildInvocation(providerInvocationOptions) reviewInvocation
	buildRestrictedInvocation(restrictedInvocationOptions) reviewInvocation
	decodeTranscript(io.Reader) (decodedTranscript, error)
	finalMessageFromTranscript() bool
}

type restrictedInvocationOptions struct {
	Session         reviewsession.NativeSession
	Plan            reviewplan.Decision
	Findings        []quality.NativeFinding
	Model           string
	ReasoningEffort string
	LeaseFile       *os.File
	SessionLockFile *os.File
	Policy          []byte
	OutputSchema    []byte
	CapturePaths    capturePaths
}

type providerInvocationOptions struct {
	Session          reviewsession.NativeSession
	Plan             reviewplan.Decision
	Goal             string
	Model            string
	ReasoningEffort  string
	ExecutionProfile string
	LeaseFile        *os.File
	SessionLockFile  *os.File
	OutputSchema     []byte
}

type decodedTranscript struct {
	FinalMessage      []byte
	InputTokens       *int64
	OutputTokens      *int64
	CachedInputTokens *int64
	UsageError        error
}

func validateProvider(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("native review provider is required")
	}
	switch provider.Host() {
	case "codex", "claude-code":
		return nil
	default:
		return fmt.Errorf("unsupported native review host %q", provider.Host())
	}
}
