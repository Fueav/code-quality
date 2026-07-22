package items

type Request struct{ ID string }

type DB interface {
	Query(statement string, arguments ...any) error
}

func Handle(db DB, request Request) error {
	return db.Query("SELECT * FROM items WHERE id = ?", request.ID)
}
