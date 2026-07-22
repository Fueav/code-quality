package workflow

type State string

type Validator interface {
	Validate(current, next State) bool
}

func Move(_ Validator, current *State, next State) bool {
	*current = next
	return true
}

func HandleTransition(validator Validator, current *State, next State) bool {
	return Move(validator, current, next)
}
