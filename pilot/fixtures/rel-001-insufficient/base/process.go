package process

type Pool interface {
	Submit(items []string, operation func(string))
}

func Process(pool Pool, items []string, operation func(string)) {
	pool.Submit(items, operation)
}

func HandleRequest(pool Pool, items []string, operation func(string)) {
	Process(pool, items, operation)
}
