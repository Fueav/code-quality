package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Fueav/code-quality/internal/intake"
)

const (
	StatusReady   = "READY"
	StatusBlocked = "BLOCKED"

	checkPass    = "PASS"
	checkBlocked = "BLOCKED"
)

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	NextAction string `json:"next_action,omitempty"`
}

type Report struct {
	SchemaVersion   int      `json:"schema_version"`
	Status          string   `json:"status"`
	Host            string   `json:"host"`
	ProviderPath    string   `json:"provider_path,omitempty"`
	ProviderVersion string   `json:"provider_version,omitempty"`
	RepositoryRoot  string   `json:"repository_root,omitempty"`
	BaseCommit      string   `json:"base_commit,omitempty"`
	TargetCommit    string   `json:"target_commit,omitempty"`
	ChangedFiles    []string `json:"changed_files"`
	DirtyWorktree   bool     `json:"dirty_worktree"`
	Checks          []Check  `json:"checks"`
	NextAction      string   `json:"next_action,omitempty"`
}

type Options struct {
	Host           string
	RepositoryPath string
	Base           string
	Target         string
	DiffReason     string
	Environment    map[string]string
}

type providerContract struct {
	host                 string
	binary               string
	authArguments        []string
	capabilityArguments  []string
	requiredCapabilities []string
}

func Run(ctx context.Context, options Options) (Report, error) {
	contract, err := contractForHost(options.Host)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: 1,
		Status:        StatusReady,
		Host:          contract.host,
		ChangedFiles:  []string{},
		Checks:        []Check{},
	}

	providerPath, lookupErr := exec.LookPath(contract.binary)
	if lookupErr != nil {
		report.block("provider_binary", fmt.Sprintf("%s is not available on PATH", contract.binary), fmt.Sprintf("install %s and rerun doctor", contract.binary))
	} else {
		report.ProviderPath = providerPath
		report.pass("provider_binary", fmt.Sprintf("using %s", providerPath))

		versionOutput, versionErr := commandOutput(ctx, providerPath, "--version")
		if versionErr != nil || strings.TrimSpace(versionOutput) == "" {
			report.block("provider_version", fmt.Sprintf("cannot read %s version", contract.binary), fmt.Sprintf("repair or update %s and rerun doctor", contract.binary))
		} else {
			report.ProviderVersion = firstLine(versionOutput)
			report.pass("provider_version", report.ProviderVersion)
		}

		capabilityOutput, capabilityErr := commandOutput(ctx, providerPath, contract.capabilityArguments...)
		missing := missingCapabilities(capabilityOutput, contract.requiredCapabilities)
		if capabilityErr != nil || len(missing) > 0 {
			report.block("provider_capabilities", fmt.Sprintf("%s lacks required CLI capabilities: %s", contract.binary, strings.Join(missing, ", ")), fmt.Sprintf("update %s and rerun doctor", contract.binary))
		} else {
			report.pass("provider_capabilities", "required native review flags are available")
		}

		authenticated, authErr := checkAuthentication(ctx, providerPath, contract)
		if authErr != nil || !authenticated {
			report.block("authentication", fmt.Sprintf("%s is not logged in", contract.binary), fmt.Sprintf("log in with %s and rerun doctor", contract.binary))
		} else {
			report.pass("authentication", fmt.Sprintf("%s authentication is available", contract.binary))
		}
	}

	discovered, discoveryErr := intake.Discover(intake.Options{
		RepositoryPath: options.RepositoryPath,
		Base:           options.Base,
		Target:         options.Target,
		DiffReason:     options.DiffReason,
		Environment:    options.Environment,
	})
	if discoveryErr != nil {
		report.RepositoryRoot = discovered.RepositoryRoot
		report.block("review_scope", discoveryErr.Error(), scopeNextAction(discoveryErr))
	} else {
		report.RepositoryRoot = discovered.RepositoryRoot
		report.BaseCommit = discovered.Request.BaseCommit
		report.TargetCommit = discovered.Request.TargetCommit
		report.ChangedFiles = append([]string(nil), discovered.Request.ChangedFiles...)
		report.DirtyWorktree = discovered.DirtyWorktree
		report.pass("review_scope", fmt.Sprintf("%d committed file(s) selected", len(report.ChangedFiles)))
		if discovered.DirtyWorktree {
			report.block("working_tree", "uncommitted changes are excluded from the review", "commit or stash working tree changes and rerun doctor")
		} else {
			report.pass("working_tree", "working tree is clean")
		}
	}

	return report, nil
}

func contractForHost(host string) (providerContract, error) {
	switch strings.TrimSpace(host) {
	case "codex":
		return providerContract{
			host: "codex", binary: "codex",
			authArguments:       []string{"login", "status"},
			capabilityArguments: []string{"exec", "--help"},
			requiredCapabilities: []string{
				"--sandbox", "--model", "--json", "--output-last-message",
			},
		}, nil
	case "claude", "claude-code":
		return providerContract{
			host: "claude-code", binary: "claude",
			authArguments:       []string{"auth", "status", "--json"},
			capabilityArguments: []string{"--help"},
			requiredCapabilities: []string{
				"--output-format", "stream-json", "--permission-mode", "auto", "--effort", "--no-session-persistence", "--model",
			},
		}, nil
	default:
		return providerContract{}, fmt.Errorf("unsupported doctor host %q; use codex or claude-code", host)
	}
}

func checkAuthentication(ctx context.Context, providerPath string, contract providerContract) (bool, error) {
	output, err := commandOutput(ctx, providerPath, contract.authArguments...)
	if err != nil {
		return false, err
	}
	if contract.host != "claude-code" {
		return true, nil
	}
	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return false, errors.New("decode Claude authentication status")
	}
	return status.LoggedIn, nil
}

func commandOutput(ctx context.Context, executable string, arguments ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, executable, arguments...)
	output, err := command.Output()
	if commandContext.Err() != nil {
		return "", commandContext.Err()
	}
	return string(output), err
}

func missingCapabilities(output string, required []string) []string {
	missing := []string{}
	for _, value := range required {
		if !strings.Contains(output, value) {
			missing = append(missing, value)
		}
	}
	return missing
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return strings.TrimSpace(line)
}

func scopeNextAction(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "origin/HEAD"):
		return "set origin/HEAD or rerun doctor with both --base and --target"
	case strings.Contains(message, "changed_files") || strings.Contains(message, "no committed changes"):
		return "commit at least one change or provide a non-empty --base and --target range"
	default:
		return "fix the reported Git review scope and rerun doctor"
	}
}

func (report *Report) pass(name, message string) {
	report.Checks = append(report.Checks, Check{Name: name, Status: checkPass, Message: message})
}

func (report *Report) block(name, message, nextAction string) {
	report.Status = StatusBlocked
	report.Checks = append(report.Checks, Check{Name: name, Status: checkBlocked, Message: message, NextAction: nextAction})
	if report.NextAction == "" {
		report.NextAction = nextAction
	}
}
