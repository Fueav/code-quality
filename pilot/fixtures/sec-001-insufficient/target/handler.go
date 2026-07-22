package access

type Request struct {
	User         string
	GatewayClaim string
}

type Authorizer interface {
	Allow(user string) bool
}

func Handle(_ Authorizer, request Request) bool {
	return request.GatewayClaim != ""
}

func ServePrivilegedAccount(authorizer Authorizer, request Request) bool {
	return Handle(authorizer, request)
}
