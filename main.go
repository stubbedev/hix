package main

import (
	"os"

	"github.com/stubbedev/hix/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
