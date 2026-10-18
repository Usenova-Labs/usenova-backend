package main

import (
	"fmt"
	"os"

	"github.com/byoi-platform/cli/cmd"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	// cmd.Root() is the executable root so service commands register as
	// "nova deploy", "nova status", ...
	rootCmd := cmd.Root()
	rootCmd.Use = "nova"
	rootCmd.Short = "Nova CLI - deploy and manage always-on backends on your own infrastructure"
	rootCmd.Long = `Nova CLI provides commands to deploy, manage, and monitor
services on your own infrastructure with Stellar payment integration.`
	rootCmd.Version = fmt.Sprintf("%s (commit: %s)", version, commit)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
