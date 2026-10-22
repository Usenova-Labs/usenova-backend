package stellar

import "testing"

const testAddress = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

func TestLinkStoresAccount(t *testing.T) {
	s := NewAccountStore()

	acct := s.Link(testAddress, "testnet", "payouts")
	if acct.Address != testAddress {
		t.Errorf("address = %q", acct.Address)
	}
	if acct.Network != "testnet" {
		t.Errorf("network = %q", acct.Network)
	}
	if acct.Label != "payouts" {
		t.Errorf("label = %q", acct.Label)
	}
	if acct.LinkedAt.IsZero() {
		t.Error("linked_at must be set")
	}

	got, err := s.Get(testAddress)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != acct {
		t.Error("Get must return the stored account")
	}
}

func TestLinkIsIdempotentPreservingLinkedAt(t *testing.T) {
	s := NewAccountStore()

	first := s.Link(testAddress, "testnet", "payouts")
	second := s.Link(testAddress, "testnet", "primary")

	if second.LinkedAt != first.LinkedAt {
		t.Error("re-linking must preserve the original LinkedAt")
	}
	if second.Label != "primary" {
		t.Errorf("label = %q, want primary (updated)", second.Label)
	}
	if len(s.List()) != 1 {
		t.Errorf("List = %d, want 1 (no duplicate)", len(s.List()))
	}
}

func TestLinkTrimsWhitespace(t *testing.T) {
	s := NewAccountStore()
	s.Link("  "+testAddress+"  ", "testnet", "")

	if _, err := s.Get(testAddress); err != nil {
		t.Fatalf("Get after whitespace trim: %v", err)
	}
}

func TestGetUnknownReturnsSentinel(t *testing.T) {
	s := NewAccountStore()
	if _, err := s.Get(testAddress); err != ErrAccountNotLinked {
		t.Errorf("err = %v, want ErrAccountNotLinked", err)
	}
}

func TestListEmptyIsNonNil(t *testing.T) {
	s := NewAccountStore()
	if got := s.List(); got == nil {
		t.Error("List must return a non-nil slice")
	}
}

func TestUnlink(t *testing.T) {
	s := NewAccountStore()
	s.Link(testAddress, "testnet", "")

	if err := s.Unlink(testAddress); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if err := s.Unlink(testAddress); err != ErrAccountNotLinked {
		t.Errorf("second Unlink err = %v, want ErrAccountNotLinked", err)
	}
}
