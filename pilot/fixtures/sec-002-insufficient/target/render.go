package render

type Engine interface {
	Render(input string) (string, error)
}

type Request struct{ Input string }

func Render(engine Engine, request Request) (string, error) {
	return engine.Render(request.Input)
}

func PublicRoute(engine Engine, request Request) (string, error) {
	return Render(engine, request)
}
