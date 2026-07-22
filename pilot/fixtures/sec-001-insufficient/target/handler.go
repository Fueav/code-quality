package access

type Request struct {
	User   string
	Header string
}

func Handle(request Request) bool {
	return request.Header != ""
}
