package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/byoi-platform/cli/internal/controlplane"
	"github.com/spf13/cobra"
)

func stellarCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stellar",
		Short: "Stellar account linking and balances",
		Long: "Link a Stellar account and query its balances through the control plane.\n" +
			"Configuration is environment-driven (STELLAR_NETWORK, STELLAR_HORIZON_URL);\n" +
			"no secret keys are handled by the CLI.",
	}
	c.AddCommand(stellarLinkCmd(), stellarAccountsCmd(), stellarBalanceCmd(), stellarUnlinkCmd())
	return c
}

func stellarLinkCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "link <address>",
		Short: "Link a Stellar account to the control plane",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cl := newControlPlaneClient()
			res, err := cl.LinkStellarAccount(ctx, args[0], label)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			if res.Account != nil {
				fmt.Printf("Linked %s (network: %s)\n", res.Account.Address, res.Account.Network)
				if res.Account.Label != "" {
					fmt.Printf("Label: %s\n", res.Account.Label)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&label, "label", "", "optional human-readable label")
	return c
}

func stellarAccountsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "accounts",
		Short: "List linked Stellar accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cl := newControlPlaneClient()
			res, err := cl.ListStellarAccounts(ctx)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			if len(res.Accounts) == 0 {
				fmt.Println("No linked accounts.")
				return nil
			}
			printAccountTable(res.Accounts)
			return nil
		},
	}
	return c
}

func stellarUnlinkCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "unlink <address>",
		Short: "Unlink a Stellar account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cl := newControlPlaneClient()
			if err := cl.UnlinkStellarAccount(ctx, args[0]); err != nil {
				return err
			}
			if output == "json" {
				return printJSON(map[string]string{"status": "unlinked", "address": args[0]})
			}
			fmt.Printf("Unlinked %s\n", args[0])
			return nil
		},
	}
	return c
}

func stellarBalanceCmd() *cobra.Command {
	var asset string
	c := &cobra.Command{
		Use:   "balance <address>",
		Short: "Query balances for a Stellar account",
		Long: "Query balances for a Stellar account via the control plane.\n" +
			"Use --asset to filter to a single asset (e.g. XLM, USDC).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cl := newControlPlaneClient()
			res, err := cl.StellarBalance(ctx, args[0], asset)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			if len(res.Balance) == 0 {
				fmt.Println("No balances found.")
				return nil
			}
			printBalanceTable(res)
			return nil
		},
	}
	c.Flags().StringVar(&asset, "asset", "", "filter to a single asset code (e.g. XLM, USDC)")
	return c
}

// printAccountTable renders linked accounts as an aligned table.
func printAccountTable(accounts []*controlplane.StellarAccount) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tNETWORK\tLABEL\tLINKED")
	for _, a := range accounts {
		label := a.Label
		if label == "" {
			label = "-"
		}
		linked := "-"
		if !a.LinkedAt.IsZero() {
			linked = a.LinkedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Address, a.Network, label, linked)
	}
	_ = tw.Flush()
}

// printBalanceTable renders balances as an aligned table.
func printBalanceTable(res *controlplane.BalanceResponse) {
	fmt.Printf("Address: %s\n", res.Address)
	fmt.Printf("Network: %s\n", res.Network)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ASSET\tISSUER\tAMOUNT")
	for _, b := range res.Balance {
		issuer := b.Issuer
		if issuer == "" {
			issuer = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", b.Asset, issuer, b.Amount)
	}
	_ = tw.Flush()
}

// formatStroops is kept for symmetry with future payment commands and is used
// in tests to verify integer amount rendering without float rounding.
func formatStroops(stroops int64) string {
	return strconv.FormatInt(stroops, 10)
}
