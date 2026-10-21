package stellar

import (
	"context"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// BalanceProvider fetches an account's balances. *stellar.Client satisfies it;
// tests inject a fake to avoid network access.
type BalanceProvider interface {
	GetAccount(ctx context.Context, address string) (*sdk.Account, error)
}

// AccountChecker validates account addresses. *stellar.Client's package-level
// ValidateAccountID is the production implementation; tests may override.
type AccountChecker func(address string) error

// Handler serves Stellar account-link and balance endpoints.
type Handler struct {
	store    *AccountStore
	provider BalanceProvider
	checker  AccountChecker
	network  sdk.Network
	logger   *logrus.Logger
}

// NewHandler builds a Handler backed by the given store and balance provider.
func NewHandler(store *AccountStore, provider BalanceProvider, network sdk.Network, logger *logrus.Logger) *Handler {
	return &Handler{
		store:    store,
		provider: provider,
		checker:  sdk.ValidateAccountID,
		network:  network,
		logger:   logger,
	}
}

// WithAccountChecker overrides address validation (used in tests).
func (h *Handler) WithAccountChecker(c AccountChecker) *Handler {
	if c != nil {
		h.checker = c
	}
	return h
}

// LinkAccount handles POST /api/v1/stellar/accounts.
func (h *Handler) LinkAccount(c *gin.Context) {
	var req LinkAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Warn("Invalid link account request")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	address := NormalizeAddress(req.Address)
	if err := h.checker(address); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid Stellar address",
			Message: err.Error(),
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"address": address,
		"network": h.network,
	}).Info("Linking Stellar account")

	acct := h.store.Link(address, string(h.network), req.Label)

	c.JSON(http.StatusCreated, LinkAccountResponse{
		Account: acct,
		Message: "Account linked successfully",
	})
}

// ListAccounts handles GET /api/v1/stellar/accounts.
func (h *Handler) ListAccounts(c *gin.Context) {
	accounts := h.store.List()
	c.JSON(http.StatusOK, AccountListResponse{
		Accounts: accounts,
		Total:    len(accounts),
	})
}

// UnlinkAccount handles DELETE /api/v1/stellar/accounts/:address.
func (h *Handler) UnlinkAccount(c *gin.Context) {
	address := NormalizeAddress(c.Param("address"))
	if address == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Address is required"})
		return
	}

	if err := h.store.Unlink(address); err != nil {
		if errors.Is(err, ErrAccountNotLinked) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "Account not linked"})
			return
		}
		h.logger.WithError(err).Error("Failed to unlink account")
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to unlink account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "unlinked", "address": address})
}

// GetBalance handles GET /api/v1/stellar/accounts/:address/balance.
// The optional ?asset= query selects a single asset (default: all balances).
func (h *Handler) GetBalance(c *gin.Context) {
	address := NormalizeAddress(c.Param("address"))
	if address == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Address is required"})
		return
	}
	if err := h.checker(address); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid Stellar address",
			Message: err.Error(),
		})
		return
	}

	ctx := c.Request.Context()
	acct, err := h.provider.GetAccount(ctx, address)
	if err != nil {
		var nf *sdk.AccountNotFoundError
		if errors.As(err, &nf) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "Account not found on network"})
			return
		}
		h.logger.WithError(err).WithField("address", address).Error("Failed to fetch balance")
		c.JSON(http.StatusBadGateway, ErrorResponse{
			Error:   "Failed to fetch balance from network",
			Message: err.Error(),
		})
		return
	}

	asset := c.Query("asset")
	entries := make([]BalanceEntry, 0, len(acct.Balances))
	for _, b := range acct.Balances {
		if asset != "" && !strings.EqualFold(b.Asset, asset) {
			continue
		}
		entries = append(entries, BalanceEntry{Asset: b.Asset, Issuer: b.Issuer, Amount: b.Amount})
	}

	c.JSON(http.StatusOK, BalanceResponse{
		Address: address,
		Network: string(h.network),
		Balance: entries,
	})
}
