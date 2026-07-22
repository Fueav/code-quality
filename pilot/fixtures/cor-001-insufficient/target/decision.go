package decision

type Request struct {
	Status      string
	AmountCents int64
}

func Allow(status string) bool {
	return status == "approved"
}

func Handle(request Request) bool {
	return Allow(request.Status)
}
