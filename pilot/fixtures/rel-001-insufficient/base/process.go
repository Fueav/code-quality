package process

type Pool interface {
	Submit(items []string)
}

func Process(pool Pool, items []string) {
	pool.Submit(items)
}
