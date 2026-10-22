package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// fakeProvider is a deterministic BalanceProvider for handler tests.
type fakeProvider struct {
	account *sdk.Account
	err     error
	calls   int
}

func (f *fakeProvider) GetAccount(_ context.Context, _ string) (*sdk.Account, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.account, nil
}

func newTestHandler(provider BalanceProvider) (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	h := NewHandler(NewAccountStore(), provider, sdk.NetworkTestnet, log)
	r := gin.New()
	accounts := r.Group("/api/v1/stellar/accounts")
	accounts.POST("", h.LinkAccount)
	accounts.GET("", h.ListAccounts)
	accounts.DELETE("/:address", h.UnlinkAccount)
	accounts.GET("/:address/balance", h.GetBalance)
	return h, r
}

func doJSON(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLinkAccountSuccess(t *testing.T) {
	_, r := newTestHandler(&fakeProvider{})

	w := doJSON(r, http.MethodPost, "/api/v1/stellar/accounts", LinkAccountRequest{
		Address: testAddress,
		Label:   "payouts",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp LinkAccountResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Account == nil || resp.Account.Address != testAddress {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp.Account.Network != "testnet" {
		t.Errorf("network = %q, want testnet", resp.Account.Network)
	}
}

func TestLinkAccountRejectsInvalidAddress(t *testing.T) {
	_, r := newTestHandler(&fakeProvider{})

	w := doJSON(r, http.MethodPost, "/api/v1/stellar/accounts", LinkAccountRequest{Address: "not-valid"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestListAccounts(t *testing.T) {
	h, r := newTestHandler(&fakeProvider{})
	h.store.Link(testAddress, "testnet", "payouts")

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp AccountListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || len(resp.Accounts) != 1 {
		t.Errorf("unexpected list: %+v", resp)
	}
}

func TestUnlinkAccount(t *testing.T) {
	h, r := newTestHandler(&fakeProvider{})
	h.store.Link(testAddress, "testnet", "")

	w := doJSON(r, http.MethodDelete, "/api/v1/stellar/accounts/"+testAddress, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	w = doJSON(r, http.MethodDelete, "/api/v1/stellar/accounts/"+testAddress, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second unlink status = %d, want 404", w.Code)
	}
}

func TestGetBalanceAllAssets(t *testing.T) {
	provider := &fakeProvider{account: &sdk.Account{
		Address: testAddress,
		Network: sdk.NetworkTestnet,
		Balances: []sdk.Balance{
			{Asset: "XLM", Amount: "100.0000000"},
			{Asset: "USDC", Issuer: "GISSUER", Amount: "25.5000000"},
		},
	}}
	_, r := newTestHandler(provider)

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts/"+testAddress+"/balance", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp BalanceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Balance) != 2 {
		t.Errorf("balances = %d, want 2", len(resp.Balance))
	}
}

func TestGetBalanceFiltersByAsset(t *testing.T) {
	provider := &fakeProvider{account: &sdk.Account{
		Address: testAddress,
		Network: sdk.NetworkTestnet,
		Balances: []sdk.Balance{
			{Asset: "XLM", Amount: "100.0000000"},
			{Asset: "USDC", Issuer: "GISSUER", Amount: "25.5000000"},
		},
	}}
	_, r := newTestHandler(provider)

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts/"+testAddress+"/balance?asset=usdc", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp BalanceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Balance) != 1 || resp.Balance[0].Asset != "USDC" {
		t.Errorf("unexpected filtered balances: %+v", resp.Balance)
	}
}

func TestGetBalanceAccountNotFound(t *testing.T) {
	provider := &fakeProvider{err: &sdk.AccountNotFoundError{Address: testAddress}}
	_, r := newTestHandler(provider)

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts/"+testAddress+"/balance", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestGetBalanceUpstreamError(t *testing.T) {
	provider := &fakeProvider{err: context.DeadlineExceeded}
	_, r := newTestHandler(provider)

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts/"+testAddress+"/balance", nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestGetBalanceInvalidAddress(t *testing.T) {
	provider := &fakeProvider{}
	_, r := newTestHandler(provider)

	w := doJSON(r, http.MethodGet, "/api/v1/stellar/accounts/bogus/balance", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if provider.calls != 0 {
		t.Error("provider must not be called for an invalid address")
	}
}
