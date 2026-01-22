package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewVersionCmd creates the version command.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("JOG version %s (commit: %s)\n", Version, Commit)
		},
	}
}
