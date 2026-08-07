package onboarding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestDoctorReadyForBothProvidersWithoutStartingAReview(t *testing.T) {
	for _, host := range []string{"codex", "claude-code"} {
		t.Run(host, func(t *testing.T) {
			repo, base, target := doctorRepository(t)
			binDir := t.TempDir()
			logPath := filepath.Join(binDir, "invocations")
			writeDoctorProvider(t, binDir, host, logPath, true, true)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			report, err := Run(context.Background(), Options{
				Host: host, RepositoryPath: repo, Base: base, Target: target,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != StatusReady || report.DirtyWorktree || len(report.ChangedFiles) != 1 || report.ProviderPath == "" || report.ProviderVersion == "" {
				t.Fatalf("report = %#v", report)
			}
			invocations, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(invocations), "exec --sandbox") || strings.Contains(string(invocations), " -p ") {
				t.Fatalf("doctor started a provider review: %s", invocations)
			}
		})
	}
}

func TestDoctorProductionCIRequiresIsolationCapabilities(t *testing.T) {
	for _, host := range []string{"codex", "claude-code"} {
		t.Run(host, func(t *testing.T) {
			repo, base, target := doctorRepository(t)
			binDir := t.TempDir()
			writeDoctorProvider(t, binDir, host, filepath.Join(binDir, "invocations"), true, true)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			report, err := Run(context.Background(), Options{
				Host: host, RepositoryPath: repo, Base: base, Target: target,
				ExecutionProfile: quality.ExecutionProfileProductionCI,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != StatusReady {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestDoctorBlocksDirtyWorktreeBeforeProviderReview(t *testing.T) {
	repo, base, target := doctorRepository(t)
	if err := os.WriteFile(filepath.Join(repo, "uncommitted.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	writeDoctorProvider(t, binDir, "codex", filepath.Join(binDir, "invocations"), true, true)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Run(context.Background(), Options{Host: "codex", RepositoryPath: repo, Base: base, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusBlocked || !report.DirtyWorktree || !strings.Contains(report.NextAction, "commit or stash") {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorBlocksMissingAuthenticationAndCapabilities(t *testing.T) {
	repo, base, target := doctorRepository(t)
	binDir := t.TempDir()
	writeDoctorProvider(t, binDir, "claude-code", filepath.Join(binDir, "invocations"), false, false)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Run(context.Background(), Options{Host: "claude-code", RepositoryPath: repo, Base: base, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusBlocked || report.NextAction == "" {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorBlocksMissingOriginHeadWithOneNextAction(t *testing.T) {
	repo, _, _ := doctorRepository(t)
	binDir := t.TempDir()
	writeDoctorProvider(t, binDir, "codex", filepath.Join(binDir, "invocations"), true, true)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Run(context.Background(), Options{Host: "codex", RepositoryPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusBlocked || !strings.Contains(report.NextAction, "origin/HEAD") {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorBlocksEmptyCommittedRange(t *testing.T) {
	repo, base, _ := doctorRepository(t)
	binDir := t.TempDir()
	writeDoctorProvider(t, binDir, "claude-code", filepath.Join(binDir, "invocations"), true, true)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Run(context.Background(), Options{Host: "claude-code", RepositoryPath: repo, Base: base, Target: base})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusBlocked || !strings.Contains(report.NextAction, "commit at least one change") {
		t.Fatalf("report = %#v", report)
	}
}

func doctorRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	runDoctorGit(t, repo, "init", "-b", "main")
	runDoctorGit(t, repo, "config", "user.email", "doctor@example.com")
	runDoctorGit(t, repo, "config", "user.name", "Doctor Test")
	if err := os.WriteFile(filepath.Join(repo, "review.go"), []byte("package review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDoctorGit(t, repo, "add", "review.go")
	runDoctorGit(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(runDoctorGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "review.go"), []byte("package review\n\nfunc Ready() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDoctorGit(t, repo, "add", "review.go")
	runDoctorGit(t, repo, "commit", "-m", "target")
	target := strings.TrimSpace(runDoctorGit(t, repo, "rev-parse", "HEAD"))
	return repo, base, target
}

func writeDoctorProvider(t *testing.T, directory, host, logPath string, authenticated, capable bool) {
	t.Helper()
	name := "codex"
	version := "codex-cli 0.145.0"
	capabilities := "--sandbox --model --json --output-last-message --output-schema"
	authOutput := "logged in"
	if host == "claude-code" {
		name = "claude"
		version = "2.1.220 (Claude Code)"
		capabilities = "--output-format stream-json --permission-mode auto plan --safe-mode --strict-mcp-config --effort --no-session-persistence --model --json-schema"
		authOutput = `{"loggedIn":true}`
	}
	if host == "codex" {
		capabilities += " read-only --ephemeral --ignore-user-config --ignore-rules --disable"
	}
	authExit := "0"
	if !authenticated {
		authExit = "1"
		authOutput = `{"loggedIn":false}`
	}
	if !capable {
		capabilities = "unsupported"
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"  '--version') printf '%s\\n' " + shellQuote(version) + ";;\n" +
		"  'login status'|'auth status --json') printf '%s\\n' " + shellQuote(authOutput) + "; exit " + authExit + ";;\n" +
		"  'exec --help'|'--help') printf '%s\\n' " + shellQuote(capabilities) + ";;\n" +
		"  *) exit 97;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runDoctorGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
