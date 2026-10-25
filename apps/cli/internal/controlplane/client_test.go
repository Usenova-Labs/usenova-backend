package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testAddress = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

func newTestClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	cl, err := New(baseURL, token, time.Second)
	if err != nil {
		t.Fatalf("New(%q): %v", baseURL, err)
	}
	return cl
}

// TestNewRejectsCleartextTokenToNonLoopback enforces CWE-319: a bearer token
// must never be sent over plaintext HTTP to a non-loopback host.
func TestNewRejectsCleartextTokenToNonLoopback(t *testing.T) {
	rejected := []struct {
		baseURL string
		token   string
	}{
		{"http://192.168.1.10:8080", "secret"},
		{"http://example.com", "secret"},
		{"http://[fd00::1]:8080", "secret"},
		{"http://control-plane.internal.", "secret"},
	}
	for _, tc := range rejected {
		if _, err := New(tc.baseURL, tc.token, time.Second); err == nil {
			t.Errorf("New(%q, token) = nil error, want plaintext/remote rejection", tc.baseURL)
		} else if !strings.Contains(err.Error(), "plaintext") {
			t.Errorf("New(%q, token) error = %v, want plaintext transport message", tc.baseURL, err)
		}
	}

	allowed := []struct {
		baseURL string
		token   string
	}{
		{"http://127.0.0.1:8080", "secret"},
		{"http://127.0.0.1", "secret"},
		{"http://localhost:8080", "secret"},
		{"http://LOCALHOST:8080", "secret"},
		{"http://[::1]:8080", "secret"},
		{"https://example.com", "secret"},
		{"http://192.168.1.10:8080", ""}, // no token: nothing to expose
		{"", "secret"},                   // no scheme: nothing to send in cleartext
	}
	for _, tc := range allowed {
		if _, err := New(tc.baseURL, tc.token, time.Second); err != nil {
			t.Errorf("New(%q, token=%q) = %v, want nil", tc.baseURL, tc.token, err)
		}
	}
}

func TestLinkStellarAccount(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(LinkAccountResponse{
			Account: &StellarAccount{Address: testAddress, Network: "testnet", Label: "payouts"},
			Message: "Account linked successfully",
		})
	}))
	defer srv.Close()

	cl := newTestClient(t, srv.URL, "token")
	res, err := cl.LinkStellarAccount(context.Background(), testAddress, "payouts")
	if err != nil {
		t.Fatalf("LinkStellarAccount: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/stellar/accounts" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["address"] != testAddress || gotBody["label"] != "payouts" {
		t.Errorf("body = %+v", gotBody)
	}
	if res.Account == nil || res.Account.Address != testAddress {
		t.Errorf("response = %+v", res)
	}
}

func TestListStellarAccounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AccountListResponse{
			Accounts: []*StellarAccount{{Address: testAddress, Network: "testnet"}},
			Total:    1,
		})
	}))
	defer srv.Close()

	cl := newTestClient(t, srv.URL, "")
	res, err := cl.ListStellarAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListStellarAccounts: %v", err)
	}
	if res.Total != 1 || len(res.Accounts) != 1 {
		t.Errorf("response = %+v", res)
	}
}

func TestStellarBalanceFiltersByAsset(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(BalanceResponse{
			Address: testAddress,
			Network: "testnet",
			Balance: []StellarBalance{{Asset: "USDC", Amount: "25.5000000"}},
		})
	}))
	defer srv.Close()

	cl := newTestClient(t, srv.URL, "")
	res, err := cl.StellarBalance(context.Background(), testAddress, "USDC")
	if err != nil {
		t.Fatalf("StellarBalance: %v", err)
	}
	if gotQuery != "asset=USDC" {
		t.Errorf("query = %q, want asset=USDC", gotQuery)
	}
	if len(res.Balance) != 1 || res.Balance[0].Asset != "USDC" {
		t.Errorf("response = %+v", res)
	}
}

func TestUnlinkStellarAccount(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"unlinked"}`))
	}))
	defer srv.Close()

	cl := newTestClient(t, srv.URL, "")
	if err := cl.UnlinkStellarAccount(context.Background(), testAddress); err != nil {
		t.Fatalf("UnlinkStellarAccount: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/stellar/accounts/"+testAddress {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestErrorResponseSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid Stellar address", Message: "too short"})
	}))
	defer srv.Close()

	cl := newTestClient(t, srv.URL, "")
	_, err := cl.LinkStellarAccount(context.Background(), "bad", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if want := "Invalid Stellar address: too short"; err.Error() == "" || !contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
	}
}

func TestStellarBalanceErrorMetadata(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          ErrorResponse
		wantStatus    int
		wantNetwork   string
		wantHorizon   string
		wantHorizonSt int
	}{
		{
			name:        "account not found",
			status:      http.StatusNotFound,
			body:        ErrorResponse{Error: "Account not found on network", Network: "testnet"},
			wantStatus:  http.StatusNotFound,
			wantNetwork: "testnet",
		},
		{
			name:        "horizon unreachable",
			status:      http.StatusBadGateway,
			body:        ErrorResponse{Error: "Failed to fetch balance from network", Message: "stellar: horizon request: dial tcp", Network: "testnet", HorizonURL: "https://horizon-testnet.stellar.org"},
			wantStatus:  http.StatusBadGateway,
			wantNetwork: "testnet",
			wantHorizon: "https://horizon-testnet.stellar.org",
		},
		{
			name:          "horizon 5xx",
			status:        http.StatusBadGateway,
			body:          ErrorResponse{Error: "Failed to fetch balance from network", Message: "stellar: horizon returned 503: unavailable", Network: "testnet", HorizonURL: "https://horizon-testnet.stellar.org", HorizonStatus: http.StatusServiceUnavailable},
			wantStatus:    http.StatusBadGateway,
			wantNetwork:   "testnet",
			wantHorizon:   "https://horizon-testnet.stellar.org",
			wantHorizonSt: http.StatusServiceUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()

			cl := newTestClient(t, srv.URL, "")
			_, err := cl.StellarBalance(context.Background(), testAddress, "")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.wantStatus)
			}
			if apiErr.Network != tc.wantNetwork {
				t.Errorf("Network = %q, want %q", apiErr.Network, tc.wantNetwork)
			}
			if apiErr.HorizonURL != tc.wantHorizon {
				t.Errorf("HorizonURL = %q, want %q", apiErr.HorizonURL, tc.wantHorizon)
			}
			if apiErr.HorizonStatus != tc.wantHorizonSt {
				t.Errorf("HorizonStatus = %d, want %d", apiErr.HorizonStatus, tc.wantHorizonSt)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
