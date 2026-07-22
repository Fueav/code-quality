package render

import "html"

type Engine interface {
	Render(input string) (string, error)
}

type Request struct{ Input string }

func Render(engine Engine, request Request) (string, error) {
	return engine.Render(html.EscapeString(request.Input))
}

func PublicRoute(engine Engine, request Request) (string, error) {
	return Render(engine, request)
}
