package config

const maxConfigurationEntries = 20

type Entry struct{ Valid bool }

func ValidateChanged(entries []Entry, changed []int) bool {
	if len(entries) > maxConfigurationEntries {
		return false
	}
	for _, entry := range entries {
		if !entry.Valid {
			return false
		}
	}
	return true
}
