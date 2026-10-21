package stellar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultRPCURL(t *testing.T) {
	cases := map[string]string{
		"testnet":   "https://soroban-testnet.stellar.org",
		"mainnet":   "https://mainnet.sorobanrpc.com",
		"public":    "https://mainnet.sorobanrpc.com",
		"futurenet": "https://rpc-futurenet.stellar.org",
		"unknown":   "https://soroban-testnet.stellar.org",
	}
	for network, want := range cases {
		if got := defaultRPCURL(network); got != want {
			t.Errorf("defaultRPCURL(%q) = %q, want %q", network, got, want)
		}
	}
}

func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client := NewClient("testnet", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM").
		WithRPCURL(srv.URL)
	return client, srv.Close
}

func TestInvokeContractSendsJSONRPC(t *testing.T) {
	var got rpcRequest
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("server decode: %v", err)
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`"Released"`),
		})
	})
	defer closeSrv()

	res, err := client.InvokeContract(context.Background(), "release", []interface{}{uint64(1)})
	if err != nil {
		t.Fatalf("InvokeContract: %v", err)
	}
	if res != "Released" {
		t.Errorf("result = %v, want Released", res)
	}
	if got.Method != "simulateTransaction" {
		t.Errorf("method = %q, want simulateTransaction", got.Method)
	}
}

func TestGetEscrowStatus(t *testing.T) {
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`"Pending"`),
		})
	})
	defer closeSrv()

	status, err := client.GetEscrowStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetEscrowStatus: %v", err)
	}
	if status != EscrowStatusPending {
		t.Errorf("status = %q, want Pending", status)
	}
}

func TestGetEscrow(t *testing.T) {
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"consumer":"C1","provider":"C2","token":"C3","amount":400,"status":"Released"}`),
		})
	})
	defer closeSrv()

	escrow, err := client.GetEscrow(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetEscrow: %v", err)
	}
	if escrow.Amount != 400 || escrow.Status != EscrowStatusReleased {
		t.Errorf("unexpected escrow: %+v", escrow)
	}
}

func TestCreateEscrowEntrypoint(t *testing.T) {
	var got rpcRequest
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`null`)})
	})
	defer closeSrv()

	_, err := client.CreateEscrow(context.Background(), EscrowRequest{
		EscrowID: 1,
		Consumer: "C1",
		Provider: "C2",
		Token:    "C3",
		Amount:   400,
	})
	if err != nil {
		t.Fatalf("CreateEscrow: %v", err)
	}

	params, ok := got.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params type = %T", got.Params)
	}
	if params["function"] != "create_escrow" {
		t.Errorf("function = %v, want create_escrow", params["function"])
	}
}

func TestRPCErrorPropagates(t *testing.T) {
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &rpcError{Code: -32600, Message: "bad request"},
		})
	})
	defer closeSrv()

	_, err := client.InvokeContract(context.Background(), "release", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetBalance(t *testing.T) {
	client, closeSrv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"balances":[{"balance":"10000000"}]}`),
		})
	})
	defer closeSrv()

	bal, err := client.GetBalance(context.Background(), "GAAAA")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal != 10000000 {
		t.Errorf("balance = %d, want 10000000", bal)
	}
}
