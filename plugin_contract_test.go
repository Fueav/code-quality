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

func TestPluginSkillRequiresApprovalAndPreservesFinalReports(t *testing.T) {
	raw, err := os.ReadFile("plugins/code-quality/skills/code-quality/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(raw)
	for _, required := range []string{
		"explicit approval",
		"Do not run `prepare` until the user approves",
		"Never delete the session directory",
		"session-local shared clone",
		"rereview_scope",
		"REVIEW_INVALID",
		"validation_errors",
	} {
		if !strings.Contains(skill, required) {
			t.Errorf("Skill is missing %q", required)
		}
	}
	if strings.Contains(skill, "rm -rf .code-quality") {
		t.Fatal("Skill must not authorize deleting the report root")
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
