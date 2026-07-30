package batch

const maxRequestItems = 20

type Item struct{ ID string }

type Request struct {
	Items [maxRequestItems]Item
}

func Process(request Request) {
	for range request.Items {
	}
}
