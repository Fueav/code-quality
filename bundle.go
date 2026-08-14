package bundle

import "embed"

//go:embed policy/manifest.json policy/v1.2/*.md schemas/*.json
var files embed.FS

func PolicyManifest() ([]byte, error) {
	return files.ReadFile("policy/manifest.json")
}

func Rubric() ([]byte, error) {
	return files.ReadFile("policy/v1.2/rubric.md")
}

func ReviewLens() ([]byte, error) {
	return files.ReadFile("policy/v1.2/review-lens.md")
}

func Workflow() ([]byte, error) {
	return files.ReadFile("policy/v1.2/workflow.md")
}

func RestrictedAdjudicationPolicy() ([]byte, error) {
	return files.ReadFile("policy/v1.2/restricted-adjudication.md")
}

func Schema(name string) ([]byte, error) {
	return files.ReadFile("schemas/" + name)
}
