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
		"dist:",
		"python3 -I -B -S scripts/release.py",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile release gate is missing %q", required)
		}
	}
	release, err := os.ReadFile("scripts/release.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"harness/repository_verification.py"), "verify", "release"`,
		`"evidence", "verify"`,
		`"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"`,
		`"plugins/code-quality/scripts/bootstrap.sh", "bootstrap.sh"`,
		`"release-manifest.json"`,
	} {
		if !strings.Contains(string(release), required) {
			t.Errorf("release packaging is missing %q", required)
		}
	}
}

func TestHarnessReleaseGateUsesNamespacedProductVersion(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "make-arguments")
	fakeMake := filepath.Join(tempDir, "make")
	if err := os.WriteFile(fakeMake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$OUTPUT\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "harness/gates/release_check.sh")
	command.Env = []string{
		"PATH=" + tempDir + ":/usr/bin:/bin",
		"OUTPUT=" + outputPath,
		"VERSION=v0.4.0",
		"CODE_QUALITY_RELEASE_VERSION=v0.5.9",
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("release gate failed: %v\n%s", err, output)
	}
	arguments, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(arguments)), "release-check VERSION=v0.5.9"; got != want {
		t.Fatalf("make arguments = %q, want %q", got, want)
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
		"args += ['--ref', version]",
		"REPOSITORY + '#' + version",
		"PLUGIN = 'code-quality@' + MARKETPLACE",
		"QUALITY_REVIEW_BIN=",
		"target = install_dir / 'quality-review'",
		"run([target, 'version'])",
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
