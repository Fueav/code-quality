package process

func Process(items []string, operation func(string)) {
	for _, item := range items {
		go operation(item)
	}
}
