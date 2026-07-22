package authorization

type Source interface {
	CanTransfer(user string) bool
}

func HandlePublicTransfer(_ Source, secondary Source, user string) bool {
	return secondary.CanTransfer(user)
}
