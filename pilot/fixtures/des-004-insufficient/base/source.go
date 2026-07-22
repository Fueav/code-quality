package authorization

type Source interface {
	CanTransfer(user string) bool
}

func HandlePublicTransfer(primary Source, user string) bool {
	return primary.CanTransfer(user)
}
