package history

type Store interface {
	LoadAllHistory() []Record
}

type Record struct{}

func Process(store Store, _ string) {
	for range store.LoadAllHistory() {
	}
}
