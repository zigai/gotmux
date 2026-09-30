//go:build integration

package tmux_test

import (
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationMessagesAndPromptHistory(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// Server Messages
	msgs, err := server.Messages(ctx, tmux.MessagesOptions{})
	if err != nil {
		t.Fatalf("server.Messages failed: %v", err)
	}

	if len(msgs) == 0 {
		t.Log("no messages returned (acceptable on fresh server)")
	}

	// Terminal capabilities
	_, err = server.Messages(ctx, tmux.MessagesOptions{Terminal: true})
	if err != nil {
		t.Fatalf("server.Messages(Terminal) failed: %v", err)
	}

	// Jobs
	_, err = server.Messages(ctx, tmux.MessagesOptions{Jobs: true})
	if err != nil {
		t.Fatalf("server.Messages(Jobs) failed: %v", err)
	}

	// Client Messages (via attached client)
	client, _, _ := uiClient(t, ctx, server, session)

	clientMsgs, err := client.Messages(ctx, tmux.MessagesOptions{})
	if err != nil {
		t.Fatalf("client.Messages failed: %v", err)
	}

	t.Logf("client.Messages count: %d", len(clientMsgs))

	// Prompt history
	_, err = server.PromptHistory(ctx, "command")
	if err != nil {
		t.Fatalf("PromptHistory failed: %v", err)
	}

	if err := server.ClearPromptHistory(ctx, "command"); err != nil {
		t.Fatalf("ClearPromptHistory failed: %v", err)
	}
}
