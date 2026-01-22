// Package cli provides CLI commands for JOG server.
package cli

import (
	"github.com/spf13/cobra"
)

var (
	// Version is set at build time.
	Version = "dev"
	// Commit is set at build time.
	Commit = "unknown"
)

// NewRootCmd creates the root command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "jog",
		Short: "JOG - Just Object Gateway",
		Long:  "JOG is an S3-compatible object storage server written in Go.",
	}

	rootCmd.AddCommand(NewServerCmd())
	rootCmd.AddCommand(NewVersionCmd())

	return rootCmd
}

// Execute runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
