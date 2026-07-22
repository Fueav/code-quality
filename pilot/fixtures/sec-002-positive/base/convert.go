package convert

import (
	"errors"
	"os/exec"
	"regexp"
)

var safeName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type Request struct{ Name string }

func HandlePublicRequest(request Request) (*exec.Cmd, error) {
	if !safeName.MatchString(request.Name) {
		return nil, errors.New("invalid name")
	}
	return exec.Command("convert", "--", request.Name), nil
}
