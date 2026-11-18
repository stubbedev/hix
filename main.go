package main

import (
	"os"

	"github.com/stubbedev/phix/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
