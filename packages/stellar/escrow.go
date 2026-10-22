package stellar

import (
	"context"
	"encoding/json"
	"fmt"
)

// EscrowStatus mirrors the on-chain EscrowStatus enum.
type EscrowStatus string

const (
	EscrowStatusPending  EscrowStatus = "Pending"
	EscrowStatusReleased EscrowStatus = "Released"
	EscrowStatusRefunded EscrowStatus = "Refunded"
)

// EscrowRequest describes the inputs required to create an escrow.
type EscrowRequest struct {
	// EscrowID is the caller-chosen unique identifier.
	EscrowID uint64
	// Consumer is the address funding the escrow.
	Consumer string
	// Provider is the address that receives funds on release.
	Provider string
	// Token is the contract id of the token used for payment.
	Token string
	// Amount is the amount in the token's smallest unit.
	Amount int64
}

// Escrow is the decoded on-chain escrow record.
type Escrow struct {
	Consumer string       `json:"consumer"`
	Provider string       `json:"provider"`
	Token    string       `json:"token"`
	Amount   int64        `json:"amount"`
	Status   EscrowStatus `json:"status"`
}

// CreateEscrow builds and invokes the create_escrow contract entrypoint.
func (c *Client) CreateEscrow(ctx context.Context, req EscrowRequest) (interface{}, error) {
	return c.InvokeContract(ctx, "create_escrow", []interface{}{
		req.EscrowID,
		req.Consumer,
		req.Provider,
		req.Token,
		req.Amount,
	})
}

// ReleaseEscrow invokes the release entrypoint, paying the provider.
func (c *Client) ReleaseEscrow(ctx context.Context, escrowID uint64) (interface{}, error) {
	return c.InvokeContract(ctx, "release", []interface{}{escrowID})
}

// RefundEscrow invokes the refund entrypoint, returning funds to the consumer.
func (c *Client) RefundEscrow(ctx context.Context, escrowID uint64) (interface{}, error) {
	return c.InvokeContract(ctx, "refund", []interface{}{escrowID})
}

// GetEscrowStatus queries the escrow status for the given id.
func (c *Client) GetEscrowStatus(ctx context.Context, escrowID uint64) (EscrowStatus, error) {
	raw, err := c.InvokeContract(ctx, "get_status", []interface{}{escrowID})
	if err != nil {
		return "", err
	}

	status, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("stellar: unexpected status payload %T", raw)
	}
	return EscrowStatus(status), nil
}

// GetEscrow queries the full escrow record for the given id.
func (c *Client) GetEscrow(ctx context.Context, escrowID uint64) (*Escrow, error) {
	raw, err := c.InvokeContract(ctx, "get_escrow", []interface{}{escrowID})
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("stellar: re-encode escrow: %w", err)
	}

	var escrow Escrow
	if err := json.Unmarshal(encoded, &escrow); err != nil {
		return nil, fmt.Errorf("stellar: decode escrow: %w", err)
	}
	return &escrow, nil
}
