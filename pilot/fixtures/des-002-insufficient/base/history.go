package history

type Store interface {
	LoadSince(checkpoint string) []Record
}

type Record struct{}

func Process(store Store, checkpoint string) {
	for range store.LoadSince(checkpoint) {
	}
}
