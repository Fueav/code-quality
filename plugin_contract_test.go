package bundle

import (
	"encoding/json"
	"os"
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
		"quality-review run-codex",
		"--goal",
		"--base",
		"--target",
		"exactly one native `codex exec review`",
		"metrics path",
		"Never delete the session directory",
	} {
		if !strings.Contains(skill, required) {
			t.Errorf("Skill is missing %q", required)
		}
	}
	for _, forbidden := range []string{"candidate-only verifier", "risk direction", "rereview_scope", "REVIEW_INVALID", "activated_rule_families", "20 rules", "rm -rf .code-quality", "all three"} {
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
		"codex plugin marketplace add Fueav/code-quality",
		"--ref v" + quality.SkillVersion,
		"codex plugin add code-quality@" + marketplaceName,
		"claude plugin marketplace add Fueav/code-quality@v" + quality.SkillVersion,
		"claude plugin install code-quality@" + marketplaceName,
	} {
		if !strings.Contains(readme, command) {
			t.Errorf("README is missing %q", command)
		}
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
