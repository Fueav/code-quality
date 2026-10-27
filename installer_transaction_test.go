package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type installationFixture struct {
	t          *testing.T
	root       string
	commands   string
	installDir string
	host       string
	env        []string
}

func newInstallationFixture(t *testing.T, host string, existing bool) *installationFixture {
	t.Helper()
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = canonicalRoot
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	commands := filepath.Join(root, "commands")
	if err := os.MkdirAll(commands, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(python, filepath.Join(commands, "python3")); err != nil {
		t.Fatal(err)
	}
	fixture := &installationFixture{t: t, root: root, commands: commands, installDir: filepath.Join(root, "install path"), host: host}
	for _, version := range []string{"0.5.12", "0.6.0"} {
		plugin := filepath.Join(root, "packages", version, "plugins", "code-quality")
		for _, descriptor := range []string{".codex-plugin", ".claude-plugin"} {
			if err := os.MkdirAll(filepath.Join(plugin, descriptor), 0o700); err != nil {
				t.Fatal(err)
			}
			writeJSONFile(t, filepath.Join(plugin, descriptor, "plugin.json"), map[string]string{"name": "code-quality", "version": version})
		}
		if err := os.WriteFile(filepath.Join(plugin, "skill.txt"), []byte("retained exact skill "+version), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, manifest := range []string{".agents/plugins", ".claude-plugin"} {
			path := filepath.Join(root, "packages", version, manifest)
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			var source any = "./plugins/code-quality"
			if strings.HasPrefix(manifest, ".agents") {
				source = map[string]string{"source": "local", "path": "./plugins/code-quality"}
			}
			writeJSONFile(t, filepath.Join(path, "marketplace.json"), map[string]any{"name": marketplaceName, "plugins": []map[string]any{{"name": "code-quality", "source": source, "version": version}}})
		}
	}
	state := map[string]any{"marketplace": nil, "plugin": nil, "unrelated": "preserve-other-plugin"}
	if existing {
		state["marketplace"] = map[string]string{"root": filepath.Join(root, "packages", "0.5.12"), "source": "https://github.com/Fueav/code-quality.git", "ref": "v0.5.12"}
		state["plugin"] = map[string]string{"path": filepath.Join(root, "packages", "0.5.12", "plugins", "code-quality"), "version": "0.5.12"}
		if err := os.MkdirAll(fixture.installDir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeContractExecutable(t, filepath.Join(fixture.installDir, "quality-review"), "#!/bin/sh\nprintf '%s\\n' 'quality-review v0.5.12'\n")
	}
	writeJSONFile(t, filepath.Join(root, "host-state.json"), state)
	configuration := filepath.Join(root, "claude-config")
	if err := os.MkdirAll(configuration, 0o700); err != nil {
		t.Fatal(err)
	}
	declarations := map[string]any{}
	if existing {
		declarations[marketplaceName] = map[string]any{"source": map[string]string{"source": "git", "url": "https://github.com/Fueav/code-quality.git", "ref": "v0.5.12"}}
	}
	writeJSONFile(t, filepath.Join(configuration, "settings.json"), map[string]any{"unrelated": "preserve-other-setting", "extraKnownMarketplaces": declarations})
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	fixture.env = append(os.Environ(), "PATH="+commands+":/usr/bin:/bin", "INSTALL_DIR="+fixture.installDir,
		"CLAUDE_CONFIG_DIR="+configuration,
		"QUALITY_REVIEW_RELEASE_BASE=https://release.invalid/v0.6.0", "FAKE_ROOT="+root, "FAKE_INSTALLER="+installer,
		"FAKE_FINAL_BINARY="+filepath.Join(fixture.installDir, "quality-review"))
	writeContractExecutable(t, filepath.Join(commands, "curl"), "#!"+python+"\n"+fakeReleaseCurl)
	writeContractExecutable(t, filepath.Join(commands, host), "#!"+python+"\n"+fakePluginHost)
	return fixture
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture *installationFixture) run(script string, extra ...string) (string, error) {
	fixture.t.Helper()
	path, err := filepath.Abs(script)
	if err != nil {
		fixture.t.Fatal(err)
	}
	args := []string{path, "v0.6.0"}
	if strings.Contains(script, "bootstrap") {
		args = append(args, fixture.host)
	}
	command := exec.Command("sh", args...)
	command.Env = append(append([]string{}, fixture.env...), extra...)
	command.Dir = fixture.root
	output, err := command.CombinedOutput()
	return string(output), err
}

func (fixture *installationFixture) state() map[string]any {
	fixture.t.Helper()
	var state map[string]any
	readContractJSON(fixture.t, filepath.Join(fixture.root, "host-state.json"), &state)
	return state
}

func (fixture *installationFixture) binaryVersion() string {
	fixture.t.Helper()
	path := filepath.Join(fixture.installDir, "quality-review")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ""
	}
	out, err := exec.Command(path, "version").CombinedOutput()
	if err != nil {
		fixture.t.Fatalf("read installed version: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func outputField(output, name string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, name+"=") {
			return strings.TrimPrefix(line, name+"=")
		}
	}
	return ""
}

func TestBootstrapTransactionInstallsOnceAndRetainsOfflineRollback(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/previous=%t", host, existing), func(t *testing.T) {
				fixture := newInstallationFixture(t, host, existing)
				output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh")
				if err != nil {
					t.Fatalf("install failed: %v\n%s", err, output)
				}
				if fixture.binaryVersion() != "quality-review v0.6.0" || fixture.state()["plugin"].(map[string]any)["version"] != "0.6.0" {
					t.Fatalf("wrong activated pair: %s", output)
				}
				if fixture.state()["unrelated"] != "preserve-other-plugin" {
					t.Fatal("modified another plugin")
				}
				rollback := outputField(output, "QUALITY_REVIEW_ROLLBACK")
				if !filepath.IsAbs(rollback) {
					t.Fatalf("missing absolute rollback command: %s", output)
				}
				before, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
				output, err = fixture.run("plugins/code-quality/scripts/bootstrap.sh")
				if err != nil || !strings.Contains(output, "INSTALL_STATUS=UNCHANGED") {
					t.Fatalf("repeat install: %v\n%s", err, output)
				}
				after, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
				delta := string(after[len(before):])
				for _, line := range strings.Split(delta, "\n") {
					if !strings.HasSuffix(line, "--help") && (strings.Contains(line, "curl ") || strings.Contains(line, "marketplace add") || strings.Contains(line, "plugin remove") || strings.Contains(line, "plugin uninstall")) {
						t.Fatalf("same-version receipt caused a reinstall: %s", delta)
					}
				}
				command := exec.Command("sh", rollback)
				command.Env = append(fixture.env, "FAKE_NO_NETWORK=1")
				if out, err := command.CombinedOutput(); err != nil {
					t.Fatalf("offline rollback: %v\n%s", err, out)
				}
				if existing {
					if fixture.binaryVersion() != "quality-review v0.5.12" || fixture.state()["plugin"].(map[string]any)["version"] != "0.5.12" {
						t.Fatal("rollback did not restore old pair")
					}
				} else if fixture.binaryVersion() != "" || fixture.state()["plugin"] != nil || fixture.state()["marketplace"] != nil {
					t.Fatal("fresh-install rollback left an installation")
				}
			})
		}
	}
}

func TestBootstrapTransactionRestoresPreviousPairAfterEachFailure(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		for _, failure := range []string{"preflight", "download", "installer-checksum", "binary-version", "plugin-remove", "marketplace-remove", "marketplace-add", "plugin-add", "verify-version", "activation-version"} {
			t.Run(host+"/"+failure, func(t *testing.T) {
				fixture := newInstallationFixture(t, host, true)
				original, _ := os.ReadFile(filepath.Join(fixture.installDir, "quality-review"))
				output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_FAILURE="+failure)
				if err == nil || strings.Contains(output, "INSTALL_STATUS=INSTALLED") {
					t.Fatalf("failure reported success: %v\n%s", err, output)
				}
				current, _ := os.ReadFile(filepath.Join(fixture.installDir, "quality-review"))
				if string(current) != string(original) {
					t.Fatalf("failure replaced old CLI: %s", output)
				}
				state := fixture.state()
				if state["plugin"] == nil || state["plugin"].(map[string]any)["version"] != "0.5.12" || state["unrelated"] != "preserve-other-plugin" {
					t.Fatalf("failure did not restore old plugin: %#v\n%s", state, output)
				}
			})
		}
	}
}

func TestBootstrapClaudeLegacyAndScopedRemovalRetainOfflineRollback(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			fixture := newInstallationFixture(t, "claude", true)
			if legacy {
				fixture.env = append(fixture.env, "FAKE_CLAUDE_LEGACY=1")
			}
			output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh")
			if err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			command := exec.Command("sh", outputField(output, "QUALITY_REVIEW_ROLLBACK"))
			command.Env = append(fixture.env, "FAKE_NO_NETWORK=1")
			command.Dir = fixture.root
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("offline rollback: %v\n%s", err, output)
			}
			if fixture.binaryVersion() != "quality-review v0.5.12" || fixture.state()["plugin"].(map[string]any)["version"] != "0.5.12" {
				t.Fatal("old CLI/plugin pair was not restored")
			}
			log, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
			removals := 0
			for _, line := range strings.Split(string(log), "\n") {
				if strings.HasPrefix(line, "claude plugin marketplace remove ") && !strings.HasSuffix(line, "--help") {
					removals++
					if strings.Contains(line, "--scope user") == legacy {
						t.Fatalf("remove did not use verified capability: %s", line)
					}
				}
			}
			if removals != 2 {
				t.Fatalf("expected install and rollback removals, got %d", removals)
			}
			var settings map[string]any
			readContractJSON(t, filepath.Join(fixture.root, "claude-config", "settings.json"), &settings)
			if settings["unrelated"] != "preserve-other-setting" {
				t.Fatal("unrelated configuration changed")
			}
		})
	}
}

func TestBootstrapClaudeLegacyFailureRestoresPreviousPair(t *testing.T) {
	for _, failure := range []string{"plugin-remove", "marketplace-remove", "marketplace-add", "plugin-add", "activation-version"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newInstallationFixture(t, "claude", true)
			output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_CLAUDE_LEGACY=1", "FAKE_FAILURE="+failure)
			if err == nil || fixture.binaryVersion() != "quality-review v0.5.12" || fixture.state()["plugin"].(map[string]any)["version"] != "0.5.12" {
				t.Fatalf("legacy failure did not restore old pair: %v\n%s", err, output)
			}
		})
	}
}

func TestBootstrapClaudeRejectsUnsupportedOrUnknownScopeBeforeUninstall(t *testing.T) {
	for _, scenario := range []string{"missing-capability", "unsupported-remove", "missing-user", "source-mismatch", "empty-source", "project-declaration", "local-declaration", "project-settings-alias", "project-directory-alias", "local-settings-alias", "unreadable-scope", "multiple-marketplaces", "unknown-marketplace", "project-plugin", "unknown-override", "relative-config"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newInstallationFixture(t, "claude", true)
			extra := []string{"FAKE_CLAUDE_LEGACY=1"}
			settings := filepath.Join(fixture.root, "claude-config", "settings.json")
			switch scenario {
			case "missing-capability":
				extra = append(extra, "FAKE_MISSING_CAPABILITY=plugin uninstall")
			case "unsupported-remove":
				extra = append(extra, "FAKE_MISSING_CAPABILITY=plugin marketplace remove")
			case "missing-user":
				writeJSONFile(t, settings, map[string]any{})
			case "source-mismatch":
				writeJSONFile(t, settings, map[string]any{"extraKnownMarketplaces": map[string]any{marketplaceName: map[string]any{"source": map[string]string{"source": "git", "url": "https://other.invalid/source"}}}})
			case "empty-source":
				writeJSONFile(t, settings, map[string]any{"extraKnownMarketplaces": map[string]any{marketplaceName: map[string]any{"source": map[string]string{"source": "git", "url": ""}}}})
				state := fixture.state()
				state["marketplace"].(map[string]any)["source"] = ""
				writeJSONFile(t, filepath.Join(fixture.root, "host-state.json"), state)
			case "project-settings-alias":
				directory := filepath.Join(fixture.root, ".claude")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(settings, filepath.Join(directory, "settings.json")); err != nil {
					t.Fatal(err)
				}
			case "project-directory-alias":
				if err := os.Symlink(filepath.Dir(settings), filepath.Join(fixture.root, ".claude")); err != nil {
					t.Fatal(err)
				}
			case "local-settings-alias":
				if err := os.Symlink(settings, filepath.Join(filepath.Dir(settings), "settings.local.json")); err != nil {
					t.Fatal(err)
				}
			case "project-declaration", "local-declaration", "unreadable-scope":
				directory := filepath.Join(fixture.root, ".claude")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				filename := "settings.json"
				if scenario == "local-declaration" {
					filename = "settings.local.json"
				}
				writeJSONFile(t, filepath.Join(directory, filename), map[string]any{"extraKnownMarketplaces": map[string]any{marketplaceName: map[string]string{"source": "unknown"}}})
				if scenario == "unreadable-scope" {
					if os.Geteuid() == 0 {
						t.Skip("root bypasses filesystem access permissions")
					}
					if err := os.Chmod(directory, 0); err != nil {
						t.Fatal(err)
					}
					defer os.Chmod(directory, 0o700)
				}
			case "multiple-marketplaces":
				extra = append(extra, "FAKE_MULTIPLE_MARKETPLACES=1")
			case "unknown-marketplace":
				extra = append(extra, "FAKE_MARKETPLACE_SCOPE=unknown")
			case "project-plugin":
				extra = append(extra, "FAKE_PLUGIN_SCOPE=project")
			case "unknown-override":
				extra = append(extra, "CLAUDE_CODE_SETTINGS_SOURCE=unknown")
			case "relative-config":
				extra = append(extra, "CLAUDE_CONFIG_DIR=claude-config")
			}
			before, _ := os.ReadFile(filepath.Join(fixture.root, "host-state.json"))
			output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", extra...)
			after, _ := os.ReadFile(filepath.Join(fixture.root, "host-state.json"))
			if err == nil || string(before) != string(after) || fixture.binaryVersion() != "quality-review v0.5.12" {
				t.Fatalf("unsafe preflight did not preserve installation: %v\n%s", err, output)
			}
			log, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
			for _, line := range strings.Split(string(log), "\n") {
				if !strings.HasSuffix(line, "--help") && (strings.Contains(line, "curl ") || strings.Contains(line, " remove ") || strings.Contains(line, " uninstall ") || strings.Contains(line, " add ") || strings.Contains(line, " install ")) {
					t.Fatalf("preflight rejection happened after mutation/download: %s", line)
				}
			}
		})
	}
}

func TestBootstrapClaudeLegacyFreshInstallPreflightsFutureRollback(t *testing.T) {
	for _, scenario := range []string{"unknown-override", "project-declaration", "plugin-add-failure"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newInstallationFixture(t, "claude", false)
			extra := []string{"FAKE_CLAUDE_LEGACY=1"}
			if scenario == "unknown-override" {
				extra = append(extra, "CLAUDE_CODE_SETTINGS_SOURCE=unknown")
			} else if scenario == "project-declaration" {
				directory := filepath.Join(fixture.root, ".claude")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				writeJSONFile(t, filepath.Join(directory, "settings.json"), map[string]any{"extraKnownMarketplaces": map[string]any{marketplaceName: map[string]string{"source": "unknown"}}})
			} else {
				extra = append(extra, "FAKE_FAILURE=plugin-add")
			}
			output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", extra...)
			state := fixture.state()
			if err == nil || state["plugin"] != nil || state["marketplace"] != nil || fixture.binaryVersion() != "" {
				t.Fatalf("fresh legacy installation was not preserved: %v %#v\n%s", err, state, output)
			}
			if scenario != "plugin-add-failure" {
				log, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
				for _, line := range strings.Split(string(log), "\n") {
					if !strings.HasSuffix(line, "--help") && (strings.Contains(line, "curl ") || strings.Contains(line, " add ") || strings.Contains(line, " install ")) {
						t.Fatalf("unsafe fresh install started before scope proof: %s", line)
					}
				}
			}
		})
	}
}

func TestBootstrapRollbackRejectsChangedHostCapabilitiesBeforeUninstall(t *testing.T) {
	fixture := newInstallationFixture(t, "claude", true)
	output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_CLAUDE_LEGACY=1")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	rollback := outputField(output, "QUALITY_REVIEW_ROLLBACK")
	before, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
	command := exec.Command("sh", rollback)
	command.Env = fixture.env // The host now advertises scoped remove, unlike the retained capability.
	command.Dir = fixture.root
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "capabilities changed") {
		t.Fatalf("changed host capability was not rejected: %v\n%s", err, output)
	}
	if fixture.binaryVersion() != "quality-review v0.6.0" || fixture.state()["plugin"].(map[string]any)["version"] != "0.6.0" {
		t.Fatal("changed host capability rollback modified the installed pair")
	}
	after, _ := os.ReadFile(filepath.Join(fixture.root, "commands.log"))
	for _, line := range strings.Split(string(after[len(before):]), "\n") {
		if line != "" && !strings.HasSuffix(line, "--help") {
			t.Fatalf("capability mismatch passed preflight: %s", line)
		}
	}
	command = exec.Command("sh", rollback)
	command.Env = append(fixture.env, "FAKE_CLAUDE_LEGACY=1", "FAKE_NO_NETWORK=1")
	command.Dir = fixture.root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rollback with original capabilities failed: %v\n%s", err, output)
	}
}

func TestBootstrapRollbackFailureIsVisibleAndRetryable(t *testing.T) {
	fixture := newInstallationFixture(t, "codex", true)
	output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_FAILURE=plugin-add", "FAKE_ROLLBACK_FAIL=1")
	if err == nil || outputField(output, "ROLLBACK_REQUIRED") == "" {
		t.Fatalf("rollback failure was hidden: %v\n%s", err, output)
	}
	command := exec.Command("sh", outputField(output, "ROLLBACK_REQUIRED"))
	command.Env = fixture.env
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("retained rollback cannot recover: %v\n%s", err, out)
	}
	if fixture.binaryVersion() != "quality-review v0.5.12" || fixture.state()["plugin"].(map[string]any)["version"] != "0.5.12" {
		t.Fatal("manual retry did not restore pair")
	}
}

func TestBootstrapFailedFirstInstallLeavesNoHalfInstallation(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		for _, failure := range []string{"marketplace-add", "plugin-add", "verify-version"} {
			t.Run(host+"/"+failure, func(t *testing.T) {
				fixture := newInstallationFixture(t, host, false)
				output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_FAILURE="+failure)
				state := fixture.state()
				if err == nil || fixture.binaryVersion() != "" || state["plugin"] != nil || state["marketplace"] != nil {
					t.Fatalf("failed first install left partial state: %v %#v\n%s", err, state, output)
				}
				if state["unrelated"] != "preserve-other-plugin" {
					t.Fatal("rollback removed another plugin")
				}
			})
		}
	}
}

func TestInstallersShareOneLockAndReceiptRequiresExactContents(t *testing.T) {
	fixture := newInstallationFixture(t, "codex", true)
	lock := filepath.Join(fixture.installDir, ".quality-review-upgrade.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"install.sh", "plugins/code-quality/scripts/bootstrap.sh"} {
		if output, err := fixture.run(script); err == nil || !strings.Contains(output, "active") {
			t.Fatalf("concurrent installer was not rejected: %v\n%s", err, output)
		}
	}
	if fixture.binaryVersion() != "quality-review v0.5.12" {
		t.Fatal("concurrent invocation changed the CLI")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh"); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	binary := filepath.Join(fixture.installDir, "quality-review")
	writeContractExecutable(t, binary, "#!/bin/sh\necho 'quality-review v0.6.0'\n# changed despite matching version\n")
	output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh")
	if err != nil || strings.Contains(output, "INSTALL_STATUS=UNCHANGED") {
		t.Fatalf("same version bypassed content verification: %v\n%s", err, output)
	}
}

func TestBootstrapSamplesPreviousPairInsideLock(t *testing.T) {
	fixture := newInstallationFixture(t, "codex", true)
	output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_UPGRADE_AFTER_PREFLIGHT=1", "FAKE_FAILURE=plugin-add")
	if err == nil || fixture.binaryVersion() != "quality-review v0.6.0" || fixture.state()["plugin"].(map[string]any)["version"] != "0.6.0" {
		t.Fatalf("rollback used stale preflight state: %v %#v\n%s", err, fixture.state(), output)
	}
}

func TestBootstrapPreservesExternalChangeDuringPreparation(t *testing.T) {
	for _, change := range []string{"cli", "host"} {
		t.Run(change, func(t *testing.T) {
			fixture := newInstallationFixture(t, "codex", true)
			output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh", "FAKE_CHANGE_DURING_DOWNLOAD="+change)
			if err == nil || !strings.Contains(output, "changed while") || strings.Contains(output, "INSTALL_STATUS=ROLLED_BACK") {
				t.Fatalf("external change was not preserved: %v\n%s", err, output)
			}
			if change == "cli" && fixture.binaryVersion() != "quality-review v9.0.0" {
				t.Fatal("overwrote the externally changed CLI")
			}
			if change == "host" && fixture.state()["plugin"].(map[string]any)["version"] != "9.0.0" {
				t.Fatal("overwrote the externally changed plugin")
			}
		})
	}
}

func TestInstallerVerifiesBeforeActivationAndRetainsRollback(t *testing.T) {
	for _, failure := range []string{"download", "binary-checksum", "binary-version", "activation-version", ""} {
		t.Run(failure, func(t *testing.T) {
			fixture := newInstallationFixture(t, "codex", true)
			output, err := fixture.run("install.sh", "FAKE_FAILURE="+failure)
			if failure != "" {
				if err == nil || fixture.binaryVersion() != "quality-review v0.5.12" {
					t.Fatalf("invalid binary replaced old one: %v\n%s", err, output)
				}
				return
			}
			if err != nil || fixture.binaryVersion() != "quality-review v0.6.0" {
				t.Fatalf("install: %v\n%s", err, output)
			}
			rollback := outputField(output, "QUALITY_REVIEW_ROLLBACK")
			command := exec.Command("sh", rollback)
			command.Env = fixture.env
			if out, err := command.CombinedOutput(); err != nil || fixture.binaryVersion() != "quality-review v0.5.12" {
				t.Fatalf("CLI rollback: %v\n%s", err, out)
			}
		})
	}
}

func TestInstallerRefusesSymlinkAndBootstrapRefusesStaleRollback(t *testing.T) {
	fixture := newInstallationFixture(t, "codex", true)
	output, err := fixture.run("plugins/code-quality/scripts/bootstrap.sh")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	binary := filepath.Join(fixture.installDir, "quality-review")
	writeContractExecutable(t, binary, "#!/bin/sh\necho 'quality-review v9.0.0'\n")
	command := exec.Command("sh", outputField(output, "QUALITY_REVIEW_ROLLBACK"))
	command.Env = fixture.env
	if out, err := command.CombinedOutput(); err == nil || !strings.Contains(string(out), "different CLI") {
		t.Fatalf("stale rollback did not protect newer version: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(binary)
	want := sha256.Sum256(before)
	if err := os.Rename(binary, binary+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary+".real", binary); err != nil {
		t.Fatal(err)
	}
	if output, err := fixture.run("install.sh"); err == nil {
		t.Fatalf("symlink replaced: %s", output)
	}
	after, _ := os.ReadFile(binary + ".real")
	got := sha256.Sum256(after)
	if hex.EncodeToString(got[:]) != hex.EncodeToString(want[:]) {
		t.Fatal("symlink target changed")
	}
}

const fakeReleaseCurl = `import hashlib,json,os,pathlib,sys
root=pathlib.Path(os.environ['FAKE_ROOT'])
with (root/'commands.log').open('a') as f: f.write('curl '+' '.join(sys.argv[1:])+'\n')
if os.environ.get('FAKE_NO_NETWORK'): sys.exit(93)
args=sys.argv[1:]; target=pathlib.Path(args[args.index('-o')+1]); url=next(a for a in args if a.startswith('https://'))
name=url.rsplit('/',1)[1]; failure=os.environ.get('FAKE_FAILURE','')
if failure=='download': sys.exit(91)
change=os.environ.get('FAKE_CHANGE_DURING_DOWNLOAD'); marker=root/'external-change'
if change and not marker.exists():
    marker.touch()
    if change=='cli': pathlib.Path(os.environ['FAKE_FINAL_BINARY']).write_text("#!/bin/sh\necho 'quality-review v9.0.0'\n")
    else:
        state=json.loads((root/'host-state.json').read_text()); state['plugin']['version']='9.0.0'; (root/'host-state.json').write_text(json.dumps(state))
installer=pathlib.Path(os.environ['FAKE_INSTALLER']).read_bytes()
version='v9.0.0' if failure=='binary-version' else 'v0.6.0'
binary=("#!/bin/sh\nprintf '%s\\n' 'quality-review "+version+"'\n").encode()
if failure=='activation-version':
    binary=b'''#!/bin/sh
if [ "$0" = "$FAKE_FINAL_BINARY" ]; then echo 'quality-review v9.0.0'; else echo 'quality-review v0.6.0'; fi
'''
if name=='checksums.txt':
    checksum=hashlib.sha256(installer).hexdigest()
    if failure=='installer-checksum': checksum='0'*64
    text=checksum+'  install.sh\n'
    for platform in ('darwin_arm64','darwin_amd64','linux_arm64','linux_amd64'):
        checksum=hashlib.sha256(binary).hexdigest() if failure!='binary-checksum' else '0'*64
        text+=checksum+'  quality-review_'+platform+'\n'
    content=text.encode()
elif name=='install.sh': content=installer
else: content=binary
target.write_bytes(content)
`

const fakePluginHost = `import json,os,pathlib,sys
root=pathlib.Path(os.environ['FAKE_ROOT']); path=root/'host-state.json'; state=json.loads(path.read_text())
host=pathlib.Path(sys.argv[0]).name; args=sys.argv[1:]
with (root/'commands.log').open('a') as f: f.write(host+' '+' '.join(args)+'\n')
failure=os.environ.get('FAKE_FAILURE',''); marker=root/'failed-once'
def save():
    path.write_text(json.dumps(state))
    if host=='claude':
        settings_path=pathlib.Path(os.environ['CLAUDE_CONFIG_DIR'])/'settings.json'
        settings=json.loads(settings_path.read_text()); declarations=settings.setdefault('extraKnownMarketplaces',{})
        source=state['marketplace']
        if source:
            value={'source':'directory','path':source['root']} if source['source'].startswith('/') else {'source':'git','url':source['source'].split('#')[0]}
            declarations['fueav-code-quality']={'source':value}
        else: declarations.pop('fueav-code-quality',None)
        settings_path.write_text(json.dumps(settings))
def fail(action):
    if failure==action and not marker.exists(): marker.touch(); sys.exit(92)
if args[-1:]==['--help']:
    text='Usage: '+host+' '+' '.join(args[:-1])+' [options]\n--json --ref --scope --keep-data'
    if host=='claude' and args[:-1]==['plugin','marketplace','remove'] and os.environ.get('FAKE_CLAUDE_LEGACY'): text=text.replace('--scope','')
    if os.environ.get('FAKE_MISSING_CAPABILITY')==' '.join(args[:-1]): text='Usage: command [options]\n'
    print(text); sys.exit(0)
if args==['plugin','marketplace','list','--json']:
    fail('preflight'); source=state['marketplace']; rows=[]
    if source:
        if host=='codex': rows=[{'name':'fueav-code-quality','root':source['root'],'marketplaceSource':{'sourceType':'git','source':source['source']}}]
        else: rows=[{'name':'fueav-code-quality','source':'directory' if source['source'].startswith('/') else 'git','url':source['source'].split('#')[0],'ref':source.get('ref'),'installLocation':source['root']}]
        if os.environ.get('FAKE_MARKETPLACE_SCOPE'): rows[0]['scope']=os.environ['FAKE_MARKETPLACE_SCOPE']
        if os.environ.get('FAKE_MULTIPLE_MARKETPLACES'): rows.append(dict(rows[0],scope='project'))
    print(json.dumps({'marketplaces':rows} if host=='codex' else rows))
elif args==['plugin','list','--json']:
    plugin=state['plugin']; rows=[]
    if plugin:
        if host=='codex': rows=[{'pluginId':'code-quality@fueav-code-quality','version':plugin['version'],'enabled':True,'source':{'source':'local','path':plugin['path']}}]
        else: rows=[{'id':'code-quality@fueav-code-quality','version':plugin['version'],'scope':os.environ.get('FAKE_PLUGIN_SCOPE','user'),'enabled':True,'installPath':plugin['path']}]
    print(json.dumps({'installed':rows} if host=='codex' else rows))
elif args[:3]==['plugin','marketplace','remove']:
    if host=='claude' and (('--scope' in args)==bool(os.environ.get('FAKE_CLAUDE_LEGACY'))): sys.exit(98)
    state['marketplace']=None; save(); fail('marketplace-remove')
elif args[:2] in (['plugin','remove'],['plugin','uninstall']):
    state['plugin']=None; save(); fail('plugin-remove')
elif args[:3]==['plugin','marketplace','add']:
    source=args[3]
    if source.startswith('/'):
        if os.environ.get('FAKE_ROLLBACK_FAIL'): sys.exit(95)
        location=pathlib.Path(source); ref=None
    else:
        if os.environ.get('FAKE_NO_NETWORK'): sys.exit(93)
        ref=args[args.index('--ref')+1] if '--ref' in args else source.rsplit('#',1)[1]
        location=root/'packages'/ref[1:]
    state['marketplace']={'root':str(location),'source':source,'ref':ref}; save(); fail('marketplace-add')
elif args[:2] in (['plugin','add'],['plugin','install']):
    location=pathlib.Path(state['marketplace']['root'])/'plugins/code-quality'
    descriptor=json.loads((location/('.codex-plugin' if host=='codex' else '.claude-plugin')/'plugin.json').read_text())
    version=descriptor['version']
    if failure=='verify-version' and not marker.exists(): version='0.0.0'; marker.touch()
    state['plugin']={'path':str(location),'version':version}; save(); fail('plugin-add')
else: sys.exit(97)
if args==['plugin','list','--json'] and os.environ.get('FAKE_UPGRADE_AFTER_PREFLIGHT') and not (root/'external-upgrade').exists():
    (root/'external-upgrade').touch(); location=root/'packages/0.6.0'
    state['marketplace']={'root':str(location),'source':'https://github.com/Fueav/code-quality.git','ref':'v0.6.0'}
    state['plugin']={'path':str(location/'plugins/code-quality'),'version':'0.6.0'}; save()
    pathlib.Path(os.environ['FAKE_FINAL_BINARY']).write_text("#!/bin/sh\nprintf '%s\\n' 'quality-review v0.6.0'\n")
`
