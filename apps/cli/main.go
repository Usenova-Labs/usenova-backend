package main

import (
	"fmt"
	"os"

	"github.com/byoi-platform/cli/cmd"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "byoi",
		Short: "BYOI Platform CLI - Deploy and manage always-on backends on your own infrastructure",
		Long: `BYOI Platform CLI provides commands to deploy, manage, and monitor
services on your own infrastructure with Stellar payment integration.`,
		Version: fmt.Sprintf("%s (commit: %s)", version, commit),
	}

	rootCmd.AddCommand(cmd.Root())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
