// Package main is the entry point for the JOG (Just Object Gateway) server.
package main

import (
	"os"

	"github.com/kumasuke/jog/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
