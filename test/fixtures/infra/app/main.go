// Package main is the payments API that the fixture deploys.
package main

import (
	"fmt"
	"os"
)

// greeting reads the listen address from the environment.
func greeting() string {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	return fmt.Sprintf("payments api on %s", addr)
}

func main() {
	fmt.Println(greeting())
}
