package process

type Pool interface {
	Submit(items []string, operation func(string))
}

func Process(_ Pool, items []string, operation func(string)) {
	for _, item := range items {
		go operation(item)
	}
}

func HandleRequest(pool Pool, items []string, operation func(string)) {
	Process(pool, items, operation)
}
