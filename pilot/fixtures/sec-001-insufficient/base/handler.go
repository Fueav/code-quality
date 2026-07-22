package access

type Request struct {
	User         string
	GatewayClaim string
}

type Authorizer interface {
	Allow(user string) bool
}

func Handle(authorizer Authorizer, request Request) bool {
	return authorizer.Allow(request.User)
}

func ServePrivilegedAccount(authorizer Authorizer, request Request) bool {
	return Handle(authorizer, request)
}
