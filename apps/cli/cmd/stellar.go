package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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

			cl, err := newControlPlaneClient()
			if err != nil {
				return err
			}
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

			cl, err := newControlPlaneClient()
			if err != nil {
				return err
			}
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

			cl, err := newControlPlaneClient()
			if err != nil {
				return err
			}
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

			cl, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			res, err := cl.StellarBalance(ctx, args[0], asset)
			if err != nil {
				return reportStellarBalanceError(args[0], err)
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

// stellarBalanceError is the structured form of a balance-query failure. It is
// printed as JSON in --output json mode and returned as the command error in
// text mode.
type stellarBalanceError struct {
	Message       string `json:"error"`
	Kind          string `json:"kind"`
	StatusCode    int    `json:"status_code,omitempty"`
	Address       string `json:"address,omitempty"`
	Network       string `json:"network,omitempty"`
	HorizonURL    string `json:"horizon_url,omitempty"`
	HorizonStatus int    `json:"horizon_status,omitempty"`
}

func (e *stellarBalanceError) Error() string { return e.Message }

// reportStellarBalanceError converts a control-plane failure into a clear,
// actionable message. JSON output still emits a structured error document.
func reportStellarBalanceError(address string, err error) error {
	e := newStellarBalanceError(address, err)
	if output == "json" {
		if jerr := printJSON(e); jerr != nil {
			return jerr
		}
	}
	return e
}

func newStellarBalanceError(address string, err error) *stellarBalanceError {
	var apiErr *controlplane.APIError
	if !errors.As(err, &apiErr) {
		// Errors outside a control-plane response come from the CLI-to-control-
		// plane hop; they say nothing about Horizon's reachability.
		return &stellarBalanceError{Kind: "control_plane_error", Message: err.Error()}
	}

	network := stellarNetworkFromEnv()
	horizonURL := stellarHorizonURL(network)
	if apiErr.Network != "" {
		network = apiErr.Network
	}
	if apiErr.HorizonURL != "" {
		horizonURL = apiErr.HorizonURL
	} else if u := horizonURLFromMessage(apiErr.Title + " " + apiErr.Message); u != "" {
		horizonURL = u
	}

	base := &stellarBalanceError{
		StatusCode: apiErr.StatusCode,
		Address:    address,
		Network:    network,
		HorizonURL: horizonURL,
	}

	detail := apiErr.Title + " " + apiErr.Message
	if apiErr.StatusCode == http.StatusNotFound && isAccountNotFound(detail) {
		base.Kind = "account_not_found"
		base.Message = fmt.Sprintf("Account %s not found on %s. Is it funded?", address, network)
		return base
	}

	status := apiErr.HorizonStatus
	if status == 0 {
		status = horizonStatusFromMessage(detail)
	}
	if status >= 500 {
		return newHorizonStatusError(base, status)
	}

	// Only treat the control-plane gateway status as an unreachable hint when
	// the envelope does not tell us what Horizon actually returned. A known
	// non-5xx Horizon status means Horizon answered (e.g. 429), not that it was
	// unreachable.
	if status == 0 {
		if indicatesHorizonUnreachable(detail) ||
			apiErr.StatusCode == http.StatusBadGateway ||
			apiErr.StatusCode == http.StatusServiceUnavailable ||
			apiErr.StatusCode == http.StatusGatewayTimeout {
			return newHorizonUnreachableError(base)
		}
	}

	base.Kind = "control_plane_error"
	base.Message = apiErr.Error()
	return base
}

func isAccountNotFound(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "account not found") ||
		strings.Contains(lower, "account does not exist")
}

func newHorizonUnreachableError(base *stellarBalanceError) *stellarBalanceError {
	base.Kind = "horizon_unreachable"
	base.Message = fmt.Sprintf("Could not reach Stellar Horizon at %s. Check your network or try again later.", base.HorizonURL)
	return base
}

func newHorizonStatusError(base *stellarBalanceError, status int) *stellarBalanceError {
	base.Kind = "horizon_status_error"
	base.HorizonStatus = status
	base.Message = fmt.Sprintf("Horizon returned an error (status %d). The testnet may be experiencing issues.", status)
	return base
}

// stellarNetworkFromEnv mirrors the Stellar package's env-driven default.
func stellarNetworkFromEnv() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("STELLAR_NETWORK"))) {
	case "mainnet", "public", "pubnet":
		return "mainnet"
	case "futurenet":
		return "futurenet"
	default:
		return "testnet"
	}
}

// stellarHorizonURL mirrors the Stellar package's env-driven Horizon default.
func stellarHorizonURL(network string) string {
	if h := strings.TrimSpace(os.Getenv("STELLAR_HORIZON_URL")); h != "" {
		return strings.TrimRight(h, "/")
	}
	switch network {
	case "mainnet":
		return "https://horizon.stellar.org"
	case "futurenet":
		return "https://horizon-futurenet.stellar.org"
	default:
		return "https://horizon-testnet.stellar.org"
	}
}

// horizonURLFromMessage extracts the first http(s) URL from a wrapped Horizon
// error, letting the CLI show the actual endpoint when older control planes do
// not include structured metadata.
func horizonURLFromMessage(msg string) string {
	lower := strings.ToLower(msg)
	start := -1
	for _, scheme := range []string{"https://", "http://"} {
		if idx := strings.Index(lower, scheme); idx >= 0 && (start == -1 || idx < start) {
			start = idx
		}
	}
	if start == -1 {
		return ""
	}
	fields := strings.Fields(msg[start:])
	if len(fields) == 0 {
		return ""
	}
	raw := strings.TrimRight(strings.TrimRight(fields[0], `).,;"'`), "/")
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}

// horizonStatusFromMessage extracts a status code from a wrapped Horizon error
// such as "stellar: horizon returned 503: ..." or "Horizon returned an error
// (status 500)."
func horizonStatusFromMessage(msg string) int {
	lower := strings.ToLower(msg)
	for _, marker := range []string{"horizon returned ", "status "} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimLeft(lower[idx+len(marker):], " (")
		rest = strings.TrimPrefix(rest, "an error (")
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 0 {
			if n, err := strconv.Atoi(rest[:i]); err == nil {
				return n
			}
		}
	}
	return 0
}

func indicatesHorizonUnreachable(msg string) bool {
	lower := strings.ToLower(msg)
	for _, marker := range []string{
		"horizon request",
		"connection refused",
		"no such host",
		"i/o timeout",
		"context deadline exceeded",
		"network is unreachable",
		"connection reset",
		"timeout awaiting response headers",
		"tls handshake timeout",
		"dial tcp",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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
