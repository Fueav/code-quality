package nativereview

import (
	"fmt"
	"io"
	"os"

	reviewsession "github.com/Fueav/code-quality/internal/session"
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
	decodeTranscript(io.Reader) (decodedTranscript, error)
	finalMessageFromTranscript() bool
}

type providerInvocationOptions struct {
	Session          reviewsession.NativeSession
	Goal             string
	Model            string
	ReasoningEffort  string
	ExecutionProfile string
	LeaseFile        *os.File
	OutputSchema     []byte
}

type decodedTranscript struct {
	FinalMessage []byte
	InputTokens  *int64
	OutputTokens *int64
	UsageError   error
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
