package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

const balanceTestAddress = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

func TestFormatStroops(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{400, "400"},
		{10000000, "10000000"},
		{-5, "-5"},
	}
	for _, tc := range cases {
		if got := formatStroops(tc.in); got != tc.want {
			t.Errorf("formatStroops(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStellarCommandTree(t *testing.T) {
	c := stellarCmd()
	if c.Name() != "stellar" {
		t.Fatalf("name = %q, want stellar", c.Name())
	}

	want := map[string]bool{"link": false, "accounts": false, "balance": false, "unlink": false}
	for _, sub := range c.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestStellarLinkRequiresAddress(t *testing.T) {
	cmd := stellarLinkCmd()
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error when no address is provided")
	}
	if err := cmd.Args(cmd, []string{"one", "two"}); err == nil {
		t.Error("expected error when too many args are provided")
	}
	if err := cmd.Args(cmd, []string{"one"}); err != nil {
		t.Errorf("unexpected error for single arg: %v", err)
	}
}

func runStellarBalance(t *testing.T, outputMode string, handler http.HandlerFunc) (string, error) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	oldCP, oldOut := controlPlaneURL, output
	controlPlaneURL, output = srv.URL, outputMode
	t.Cleanup(func() {
		controlPlaneURL, output = oldCP, oldOut
	})

	c := stellarBalanceCmd()
	c.SilenceUsage = true
	c.SilenceErrors = true
	c.SetArgs([]string{balanceTestAddress})

	return captureStdout(t, func() error { return c.Execute() })
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	runErr := fn()
	_ = w.Close()

	b, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	return string(b), runErr
}

func TestStellarBalanceAccountNotFound(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "testnet")
	t.Setenv("STELLAR_HORIZON_URL", "")

	_, err := runStellarBalance(t, "text", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "Account not found on network",
			"network": "testnet",
		})
	})

	want := "Account " + balanceTestAddress + " not found on testnet. Is it funded?"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestStellarBalanceHorizonUnreachable(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "testnet")
	t.Setenv("STELLAR_HORIZON_URL", "")

	_, err := runStellarBalance(t, "text", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":       "Failed to fetch balance from network",
			"message":     "stellar: horizon request: dial tcp: connection refused",
			"network":     "testnet",
			"horizon_url": "https://horizon-testnet.stellar.org",
		})
	})

	want := "Could not reach Stellar Horizon at https://horizon-testnet.stellar.org. Check your network or try again later."
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestStellarBalanceHorizon5xx(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "testnet")
	t.Setenv("STELLAR_HORIZON_URL", "")

	_, err := runStellarBalance(t, "text", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":          "Failed to fetch balance from network",
			"message":        "stellar: horizon returned 503: unavailable",
			"network":        "testnet",
			"horizon_url":    "https://horizon-testnet.stellar.org",
			"horizon_status": http.StatusServiceUnavailable,
		})
	})

	want := "Horizon returned an error (status 503). The testnet may be experiencing issues."
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestStellarBalanceHorizon5xxFromMessage(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "")
	t.Setenv("STELLAR_HORIZON_URL", "")

	_, err := runStellarBalance(t, "text", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "Failed to fetch balance from network",
			"message": "stellar: horizon returned 500: internal server error",
		})
	})

	want := "Horizon returned an error (status 500). The testnet may be experiencing issues."
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestStellarBalanceJSONErrorObject(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "testnet")
	t.Setenv("STELLAR_HORIZON_URL", "")

	stdout, err := runStellarBalance(t, "json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "Account not found on network",
			"network": "testnet",
		})
	})

	want := "Account " + balanceTestAddress + " not found on testnet. Is it funded?"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	var got map[string]any
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not valid JSON (%v): %q", jerr, stdout)
	}
	if got["error"] != want {
		t.Errorf("json error = %v, want %q", got["error"], want)
	}
	if got["kind"] != "account_not_found" {
		t.Errorf("json kind = %v, want account_not_found", got["kind"])
	}
	if got["status_code"] != float64(http.StatusNotFound) {
		t.Errorf("json status_code = %v, want %d", got["status_code"], http.StatusNotFound)
	}
}
