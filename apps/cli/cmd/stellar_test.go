package cmd

import "testing"

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
