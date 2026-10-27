package main

import (
	"os"

	"github.com/hakanor/proxypilot/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
