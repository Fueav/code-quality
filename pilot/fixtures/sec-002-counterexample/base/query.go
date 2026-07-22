package items

type DB interface {
	LookupValidated(id string) error
}

func Handle(db DB, id string) error {
	return db.LookupValidated(id)
}
