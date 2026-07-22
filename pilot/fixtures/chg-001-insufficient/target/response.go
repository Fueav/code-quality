package main

import "fmt"

func PublicResponse() string {
	return `{"id":1}`
}

func main() { fmt.Print(PublicResponse()) }
