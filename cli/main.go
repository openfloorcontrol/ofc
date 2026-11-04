// Package main is the entry point for the ofc CLI.
package main

import (
	"fmt"
	"os"

	"github.com/openfloorcontrol/ofc/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
