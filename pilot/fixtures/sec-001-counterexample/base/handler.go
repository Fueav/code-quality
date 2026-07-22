package access

type User struct {
	TenantID string
	Role     string
}

func Handle(user User) bool {
	return user.Role == "editor"
}
