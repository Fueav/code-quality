package workflow

type State string

func Move(current *State, next State) bool {
	*current = next
	return true
}
