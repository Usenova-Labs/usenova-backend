package stellar

// LinkAccountRequest is the body for POST /api/v1/stellar/accounts.
type LinkAccountRequest struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
}

// LinkAccountResponse is returned after linking an account.
type LinkAccountResponse struct {
	Account *LinkedAccount `json:"account"`
	Message string         `json:"message,omitempty"`
}

// AccountListResponse is the payload for GET /api/v1/stellar/accounts.
type AccountListResponse struct {
	Accounts []*LinkedAccount `json:"accounts"`
	Total    int              `json:"total"`
}

// BalanceEntry is one asset balance in a BalanceResponse.
type BalanceEntry struct {
	Asset  string `json:"asset"`
	Issuer string `json:"issuer,omitempty"`
	Amount string `json:"amount"`
}

// BalanceResponse is the payload for a balance query.
type BalanceResponse struct {
	Address string         `json:"address"`
	Network string         `json:"network"`
	Balance []BalanceEntry `json:"balances"`
}

// ErrorResponse mirrors the control plane's standard error envelope.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
	// Network is the configured Stellar network for stellar-related errors.
	Network string `json:"network,omitempty"`
	// HorizonURL and HorizonStatus expose upstream Horizon details so clients
	// can present a clear, actionable message.
	HorizonURL    string `json:"horizon_url,omitempty"`
	HorizonStatus int    `json:"horizon_status,omitempty"`
}
