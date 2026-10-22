package stellar

import (
	"strings"
	"sync"
	"time"
)

// LinkedAccount is a Stellar account associated with a consumer, optionally
// with a human-readable label. Only the public address is stored; signing keys
// are never handled by the control plane.
type LinkedAccount struct {
	Address  string    `json:"address"`
	Network  string    `json:"network"`
	Label    string    `json:"label,omitempty"`
	LinkedAt time.Time `json:"linked_at"`
}

// AccountStore is a thread-safe in-memory store of linked Stellar accounts
// keyed by address.
type AccountStore struct {
	mu       sync.RWMutex
	accounts map[string]*LinkedAccount
}

// NewAccountStore creates an empty AccountStore.
func NewAccountStore() *AccountStore {
	return &AccountStore{accounts: make(map[string]*LinkedAccount)}
}

// NormalizeAddress canonicalises an address for use as a map key. Stellar
// StrKeys are case-sensitive base32; trimming surrounding whitespace is the
// only safe normalisation.
func NormalizeAddress(address string) string {
	return strings.TrimSpace(address)
}

// Link stores (or updates the label of) a linked account. Linking the same
// address twice is idempotent: the label is updated and LinkedAt is preserved.
func (s *AccountStore) Link(address, network, label string) *LinkedAccount {
	key := NormalizeAddress(address)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.accounts[key]; ok {
		if label != "" {
			existing.Label = label
		} else if existing.Label == "" {
			existing.Label = label
		}
		return existing
	}

	acct := &LinkedAccount{
		Address:  key,
		Network:  network,
		Label:    label,
		LinkedAt: time.Now().UTC(),
	}
	s.accounts[key] = acct
	return acct
}

// Get returns a linked account by address.
func (s *AccountStore) Get(address string) (*LinkedAccount, error) {
	key := NormalizeAddress(address)

	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, ok := s.accounts[key]
	if !ok {
		return nil, ErrAccountNotLinked
	}
	return acct, nil
}

// List returns all linked accounts.
func (s *AccountStore) List() []*LinkedAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*LinkedAccount, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, a)
	}
	return out
}

// Unlink removes a linked account. It returns ErrAccountNotLinked when the
// address was not linked.
func (s *AccountStore) Unlink(address string) error {
	key := NormalizeAddress(address)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.accounts[key]; !ok {
		return ErrAccountNotLinked
	}
	delete(s.accounts, key)
	return nil
}

// AccountError is a sentinel-comparable store error.
type AccountError struct {
	Message string
}

func (e *AccountError) Error() string { return e.Message }

// ErrAccountNotLinked is returned when an address has not been linked.
var ErrAccountNotLinked = &AccountError{"account not linked"}
