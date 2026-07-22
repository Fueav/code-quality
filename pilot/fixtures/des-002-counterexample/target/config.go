package config

const maxConfigurationEntries = 128

type Document struct {
	Entries [maxConfigurationEntries]bool
}

func ValidateAll(document Document) bool {
	for _, valid := range document.Entries {
		if !valid {
			return false
		}
	}
	return true
}
