package workflow

type State string

type Validator interface {
	Validate(current, next State) bool
}

func Move(validator Validator, current *State, next State) bool {
	if !validator.Validate(*current, next) {
		return false
	}
	*current = next
	return true
}

func HandleTransition(validator Validator, current *State, next State) bool {
	return Move(validator, current, next)
}
