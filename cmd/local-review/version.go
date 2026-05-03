package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set via -ldflags during build (e.g., -X main.version=v0.1.0)
// Default to "dev" for local builds.
var version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("local-review %s\n", version)
		},
	}
}
