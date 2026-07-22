package render

type Engine interface {
	Render(input string) (string, error)
}

func Render(engine Engine, input string) (string, error) {
	return engine.Render(input)
}
