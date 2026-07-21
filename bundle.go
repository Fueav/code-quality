package bundle

import "embed"

//go:embed policy schemas/*.json
var files embed.FS

func PolicyManifest() ([]byte, error) {
	return files.ReadFile("policy/manifest.json")
}

func Rubric() ([]byte, error) {
	return files.ReadFile("policy/v1.1/rubric.md")
}

func Schema(name string) ([]byte, error) {
	return files.ReadFile("schemas/" + name)
}
