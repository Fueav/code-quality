package items

type DB interface {
	LookupValidated(id string) error
	Query(statement string, arguments ...any) error
}

func Handle(db DB, id string) error {
	return db.LookupValidated(id)
}
