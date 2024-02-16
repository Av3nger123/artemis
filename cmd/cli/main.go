package main

import (
	"artemis/pkg/cli"
	"fmt"
	"os"
)

func main() {
	cli.Init()
	if err := cli.RootCmd.Execute(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}
