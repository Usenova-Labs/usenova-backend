package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testAddress = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

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

	cl := New(srv.URL, "token", time.Second)
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

	cl := New(srv.URL, "", time.Second)
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

	cl := New(srv.URL, "", time.Second)
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

	cl := New(srv.URL, "", time.Second)
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

	cl := New(srv.URL, "", time.Second)
	_, err := cl.LinkStellarAccount(context.Background(), "bad", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if want := "Invalid Stellar address: too short"; err.Error() == "" || !contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
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
