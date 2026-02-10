package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Create a new blueprint",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := "my-floor"
		if len(args) > 0 {
			name = args[0]
		}

		filename := "blueprint.yaml"
		if _, err := os.Stat(filename); err == nil {
			fmt.Fprintf(os.Stderr, "Error: %s already exists\n", filename)
			os.Exit(1)
		}

		template := fmt.Sprintf(`# OFC Blueprint - %s
# Run with: ofc run

name: %s
description: "Describe your floor here"

defaults:
  endpoint: http://localhost:11434/v1
  model: llama3

agents:
  - id: "@assistant"
    name: "Assistant"
    activation: always
    can_use_tools: false
    temperature: 0.7
    prompt: |
      You are a helpful assistant.
      Keep responses concise and helpful.
`, name, name)

		if err := os.WriteFile(filename, []byte(template), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating blueprint: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created %s\n", filename)
		fmt.Println("Run with: ofc run")
	},
}
