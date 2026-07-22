package access

type Request struct {
	User   string
	Header string
}

type Authorizer interface {
	Allow(user string) bool
}

func Handle(_ Authorizer, request Request) bool {
	return request.Header != ""
}

func Serve(authorizer Authorizer, request Request) bool {
	return Handle(authorizer, request)
}
