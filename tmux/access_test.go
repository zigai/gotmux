package tmux

import (
	"errors"
	"testing"
)

func TestParseAccessLine(t *testing.T) {
	tests := []struct {
		input    string
		expected AccessEntry
		ok       bool
	}{
		{
			input: "zigai (U,W)",
			expected: AccessEntry{
				Name:     "zigai",
				IsGroup:  false,
				ReadOnly: false,
			},
			ok: true,
		},
		{
			input: "users (G,R)",
			expected: AccessEntry{
				Name:     "users",
				IsGroup:  true,
				ReadOnly: true,
			},
			ok: true,
		},
		{
			input: "admin (U,R)",
			expected: AccessEntry{
				Name:     "admin",
				IsGroup:  false,
				ReadOnly: true,
			},
			ok: true,
		},
		{
			input: "wheel (G,W)",
			expected: AccessEntry{
				Name:     "wheel",
				IsGroup:  true,
				ReadOnly: false,
			},
			ok: true,
		},
		{
			input: "user with spaces (U,W)",
			expected: AccessEntry{
				Name:     "user with spaces",
				IsGroup:  false,
				ReadOnly: false,
			},
			ok: true,
		},
		{input: "", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "invalid line", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "nobody (X)", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "(U,W)", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: true},
	}

	for _, tc := range tests {
		got, ok := parseAccessLine(tc.input)
		if ok != tc.ok {
			t.Errorf("parseAccessLine(%q) ok = %v, want %v", tc.input, ok, tc.ok)
			continue
		}

		if ok && got != tc.expected {
			t.Errorf("parseAccessLine(%q) = %+v, want %+v", tc.input, got, tc.expected)
		}
	}
}

func TestServerAccessValidation(t *testing.T) {
	ctx := t.Context()

	var nilServer *Server

	if _, err := nilServer.AccessList(ctx); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.AccessList, got: %v", err)
	}

	if err := nilServer.GrantAccess(ctx, "user", AccessOptions{IsGroup: false, ReadOnly: false}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.GrantAccess, got: %v", err)
	}

	if err := nilServer.RevokeAccess(ctx, "user", RevokeAccessOptions{IsGroup: false}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.RevokeAccess, got: %v", err)
	}

	s := localServer(t)

	invalidNames := []string{"", "\x00bad", "bad\x00user"}
	for _, name := range invalidNames {
		if err := s.GrantAccess(ctx, name, AccessOptions{IsGroup: false, ReadOnly: false}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("expected ErrInvalidArgument on GrantAccess(%q), got: %v", name, err)
		}

		if err := s.RevokeAccess(ctx, name, RevokeAccessOptions{IsGroup: false}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("expected ErrInvalidArgument on RevokeAccess(%q), got: %v", name, err)
		}
	}
}
