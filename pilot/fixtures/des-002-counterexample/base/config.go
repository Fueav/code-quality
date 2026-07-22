package config

type Entry struct{ Valid bool }

func ValidateChanged(entries []Entry, changed []int) bool {
	for _, index := range changed {
		if !entries[index].Valid {
			return false
		}
	}
	return true
}
