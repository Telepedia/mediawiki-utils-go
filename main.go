package main

import (
	"fmt"
	"os"

	"github.com/telepedia/mediawiki-utils-go/internal"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("incorrect number of arguments passed, expected 'deploy' or 'utils' subcommand")
		os.Exit(1)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "deploy":
		internal.RunDeploy(os.Args[2:])
	case "utils":
		internal.RunUtil(os.Args[2:])
	default:
		fmt.Println("unknown subcommand:", subcommand)
		os.Exit(1)
	}
}
