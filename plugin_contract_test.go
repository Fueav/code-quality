package bundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

const marketplaceName = "fueav-code-quality"

func TestHostMarketplacesExposeCodeQualityPlugin(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		var manifest struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name   string `json:"name"`
				Source struct {
					Kind string `json:"source"`
					Path string `json:"path"`
				} `json:"source"`
			} `json:"plugins"`
		}
		readContractJSON(t, ".agents/plugins/marketplace.json", &manifest)
		if manifest.Name != marketplaceName || len(manifest.Plugins) != 1 ||
			manifest.Plugins[0].Name != "code-quality" ||
			manifest.Plugins[0].Source.Kind != "local" ||
			manifest.Plugins[0].Source.Path != "./plugins/code-quality" {
			t.Fatalf("Codex marketplace contract drifted: %#v", manifest)
		}
	})

	t.Run("claude-code", func(t *testing.T) {
		var manifest struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name    string `json:"name"`
				Source  string `json:"source"`
				Version string `json:"version"`
			} `json:"plugins"`
		}
		readContractJSON(t, ".claude-plugin/marketplace.json", &manifest)
		if manifest.Name != marketplaceName || len(manifest.Plugins) != 1 ||
			manifest.Plugins[0].Name != "code-quality" ||
			manifest.Plugins[0].Source != "./plugins/code-quality" ||
			manifest.Plugins[0].Version != quality.SkillVersion {
			t.Fatalf("Claude Code marketplace contract drifted: %#v", manifest)
		}
	})
}

func TestPluginSkillUsesThinNativeReviewPath(t *testing.T) {
	raw, err := os.ReadFile("plugins/code-quality/skills/code-quality/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(raw)
	for _, required := range []string{
		"<bin> run-codex",
		"<bin> run-claude",
		"<bin> doctor --host codex",
		"<bin> doctor --host claude-code",
		"--goal",
		"--base",
		"--target",
		"恰好一次顶层原生 Provider 调用",
		"不得削减宿主工具、MCP、插件、设置或上下文",
		"raw freeze path",
		"metrics path",
		"不要删除 session 或报告",
		"遇到活动审查不要重试或绕过",
		"不要重试或绕过",
		"输出精确等于 `quality-review v" + quality.SkillVersion + "`",
		"版本不一致",
		"https://github.com/Fueav/code-quality/releases/download/v" + quality.SkillVersion + "/bootstrap.sh",
		"系统临时区",
		"绝对 `--output-root`",
	} {
		if !strings.Contains(skill, required) {
			t.Errorf("Skill is missing %q", required)
		}
	}
	for _, forbidden := range []string{"本插件 `scripts/bootstrap.sh", "candidate-only verifier", "risk direction", "rereview_scope", "REVIEW_INVALID", "activated_rule_families", "20 rules", "rm -rf .code-quality", "all three", "CODE_QUALITY_NATIVE_DISCOVERY_MARKER", ".code-quality-native-discovery-child-v1", "process-ancestry"} {
		if strings.Contains(skill, forbidden) {
			t.Fatalf("Skill retains obsolete runtime guidance %q", forbidden)
		}
	}
}

func TestPluginDescriptorsMatchRuntimeVersion(t *testing.T) {
	for _, path := range []string{
		"plugins/code-quality/.claude-plugin/plugin.json",
		"plugins/code-quality/.codex-plugin/plugin.json",
	} {
		var descriptor struct {
			Version string `json:"version"`
		}
		readContractJSON(t, path, &descriptor)
		if descriptor.Version != quality.SkillVersion {
			t.Errorf("%s version = %q, want %q", path, descriptor.Version, quality.SkillVersion)
		}
	}
}

func TestReleaseTagMatchesRuntimeVersion(t *testing.T) {
	tag := strings.TrimSpace(os.Getenv("CODE_QUALITY_RELEASE_TAG"))
	if tag == "" {
		return
	}
	if tag != "v"+quality.SkillVersion {
		t.Fatalf("release tag = %q, want v%s", tag, quality.SkillVersion)
	}
}

func TestPluginDescriptorsDescribeCurrentPolicy(t *testing.T) {
	for _, path := range []string{
		"plugins/code-quality/.claude-plugin/plugin.json",
		"plugins/code-quality/.codex-plugin/plugin.json",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		descriptor := string(raw)
		if strings.Contains(descriptor, "V1.1") || !strings.Contains(descriptor, "V1.2") {
			t.Errorf("%s does not consistently describe V1.2", path)
		}
	}
}

func TestMakefileReleaseGateCoversShippedComponents(t *testing.T) {
	raw, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(raw)
	for _, required := range []string{
		"release-check:",
		"CODE_QUALITY_RELEASE_TAG",
		"go vet ./...",
		"$(MAKE) live-test",
		"$(MAKE) mining-test",
		"$(MAKE) qualification-test",
		"git diff --check",
		"dist: release-check",
		"RELEASE_BINARIES :=",
		"@set -e; for p in $(PLATFORMS)",
		"shasum -a 256 $(RELEASE_BINARIES)",
		"cp plugins/code-quality/scripts/bootstrap.sh dist/bootstrap.sh",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile release gate is missing %q", required)
		}
	}
}

func TestReadmeProvidesCopyPastePluginInstallCommands(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	for _, command := range []string{
		"sh -s -- v" + quality.SkillVersion,
		"bootstrap.sh | sh -s -- v" + quality.SkillVersion + " codex",
		"bootstrap.sh | sh -s -- v" + quality.SkillVersion + " claude",
		"codex plugin marketplace add Fueav/code-quality",
		"--ref v" + quality.SkillVersion,
		"codex plugin add code-quality@" + marketplaceName,
		"claude plugin marketplace add https://github.com/Fueav/code-quality.git#v" + quality.SkillVersion,
		"claude plugin install code-quality@" + marketplaceName,
		"quality-review doctor --host codex",
		"quality-review doctor --host claude-code",
		"quality-review run-claude",
		"请为当前仓库安装并运行 Fueav code-quality v" + quality.SkillVersion,
		"固定版本安装入口是 https://github.com/Fueav/code-quality/releases/download/v" + quality.SkillVersion + "/bootstrap.sh",
		"升级时优先直接重跑上面的固定版本 bootstrap",
		"系统临时区",
		"权限为 `0700`",
	} {
		if !strings.Contains(readme, command) {
			t.Errorf("README is missing %q", command)
		}
	}
}

func TestBootstrapPinsBothHostPluginsAndReturnsAbsoluteBinary(t *testing.T) {
	raw, err := os.ReadFile("plugins/code-quality/scripts/bootstrap.sh")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := string(raw)
	for _, required := range []string{
		"Fueav/code-quality",
		"--ref \"$VERSION\"",
		"https://github.com/Fueav/code-quality.git#${VERSION}",
		"PLUGIN=\"code-quality@${MARKETPLACE}\"",
		"QUALITY_REVIEW_BIN=",
		"${install_dir_abs}/quality-review",
		"installed_version=$(\"$review_bin\" version)",
	} {
		if !strings.Contains(bootstrap, required) {
			t.Errorf("bootstrap is missing %q", required)
		}
	}
	for _, forbidden := range []string{".zshrc", ".bashrc", "git@github.com"} {
		if strings.Contains(bootstrap, forbidden) {
			t.Errorf("bootstrap must not contain %q", forbidden)
		}
	}
}

func TestBootstrapExecutesPinnedInstallAndHostUpgrade(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, "commands")
			installDir := "relative install dir"
			if err := os.MkdirAll(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			installer := filepath.Join(root, "installer.sh")
			writeContractExecutable(t, installer, `#!/bin/sh
set -eu
mkdir -p "$INSTALL_DIR"
cat > "$INSTALL_DIR/quality-review" <<'EOF'
#!/bin/sh
printf '%s\n' 'quality-review v0.5.0'
EOF
chmod +x "$INSTALL_DIR/quality-review"
`)
			writeContractExecutable(t, filepath.Join(binDir, "curl"), `#!/bin/sh
set -eu
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then output="$2"; shift 2; continue; fi
  shift
done
test -n "$output"
cp "$FAKE_INSTALLER" "$output"
`)
			logPath := filepath.Join(root, "host.log")
			statePath := filepath.Join(root, "state")
			writeContractExecutable(t, filepath.Join(binDir, "codex"), `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_HOST_LOG"
case "$*" in
  'plugin marketplace list --json')
    if [ -f "$FAKE_HOST_STATE" ]; then printf '%s\n' '{"name": "fueav-code-quality"}'; else printf '%s\n' '{"marketplaces": []}'; fi ;;
  'plugin marketplace add Fueav/code-quality --ref v0.5.0') touch "$FAKE_HOST_STATE" ;;
  'plugin marketplace remove fueav-code-quality') rm -f "$FAKE_HOST_STATE" ;;
  'plugin add code-quality@fueav-code-quality'|'plugin remove code-quality@fueav-code-quality') ;;
  *) exit 97 ;;
esac
`)
			writeContractExecutable(t, filepath.Join(binDir, "claude"), `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_HOST_LOG"
case "$*" in
  'plugin marketplace add https://github.com/Fueav/code-quality.git#v0.5.0') ;;
  'plugin list') if [ -f "$FAKE_HOST_STATE" ]; then printf '%s\n' 'code-quality@fueav-code-quality'; fi ;;
  'plugin install code-quality@fueav-code-quality --scope user') touch "$FAKE_HOST_STATE" ;;
  'plugin update code-quality@fueav-code-quality --scope user') ;;
  *) exit 97 ;;
esac
`)

			bootstrapPath, err := filepath.Abs("plugins/code-quality/scripts/bootstrap.sh")
			if err != nil {
				t.Fatal(err)
			}
			for runNumber := 0; runNumber < 2; runNumber++ {
				command := exec.Command("sh", bootstrapPath, "v0.5.0", host)
				command.Dir = root
				command.Env = append(os.Environ(),
					"PATH="+binDir+string(os.PathListSeparator)+"/usr/bin:/bin",
					"HOME="+root,
					"INSTALL_DIR="+installDir,
					"QUALITY_REVIEW_RELEASE_BASE=https://release.invalid/v0.5.0",
					"FAKE_INSTALLER="+installer,
					"FAKE_HOST_LOG="+logPath,
					"FAKE_HOST_STATE="+statePath,
				)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("bootstrap run %d: %v: %s", runNumber+1, err, output)
				}
				canonicalRoot, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				expectedBinary := filepath.Join(canonicalRoot, installDir, "quality-review")
				if !strings.Contains(string(output), "QUALITY_REVIEW_BIN="+expectedBinary) ||
					!strings.Contains(string(output), "NEXT_COMMAND=\""+expectedBinary+"\" doctor") {
					t.Fatalf("bootstrap output = %s", output)
				}
			}

			logBytes, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			log := string(logBytes)
			if host == "codex" {
				for _, expected := range []string{
					"plugin marketplace add Fueav/code-quality --ref v0.5.0",
					"plugin add code-quality@fueav-code-quality",
					"plugin marketplace remove fueav-code-quality",
				} {
					if !strings.Contains(log, expected) {
						t.Errorf("Codex bootstrap log is missing %q: %s", expected, log)
					}
				}
			} else {
				for _, expected := range []string{
					"plugin marketplace add https://github.com/Fueav/code-quality.git#v0.5.0",
					"plugin install code-quality@fueav-code-quality --scope user",
					"plugin update code-quality@fueav-code-quality --scope user",
				} {
					if !strings.Contains(log, expected) {
						t.Errorf("Claude bootstrap log is missing %q: %s", expected, log)
					}
				}
			}
		})
	}
}

func writeContractExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readContractJSON(t *testing.T, path string, destination any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		t.Fatal(err)
	}
}
