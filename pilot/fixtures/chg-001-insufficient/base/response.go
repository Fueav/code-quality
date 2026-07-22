package main

import "fmt"

func PublicResponse() string {
	return `{"id":1,"legacy_name":"x"}`
}

func main() { fmt.Print(PublicResponse()) }
