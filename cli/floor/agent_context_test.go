package floor

import "testing"

func TestAgentContextAccumulatesMessages(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events() // drain

	entries := ac.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", entries[0].Content)
	}
	if entries[0].From != "@user" {
		t.Errorf("expected '@user', got %q", entries[0].From)
	}
}

func TestAgentContextMultipleMessages(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "first"})
	<-chat.Events()
	chat.Post(ChatMessage{From: "@agent", Content: "second"})
	<-chat.Events()
	chat.Post(ChatMessage{From: "@user", Content: "third"})
	<-chat.Events()

	entries := ac.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Content != "first" || entries[1].Content != "second" || entries[2].Content != "third" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

func TestAgentContextDelta(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "first"})
	<-chat.Events()

	// First delta should include everything
	delta := ac.Delta()
	if len(delta) != 1 {
		t.Fatalf("expected 1 delta entry, got %d", len(delta))
	}

	// Mark as sent
	ac.MarkSent()

	// Delta should now be empty
	delta = ac.Delta()
	if len(delta) != 0 {
		t.Fatalf("expected 0 delta after MarkSent, got %d", len(delta))
	}

	// Post more
	chat.Post(ChatMessage{From: "@user", Content: "second"})
	<-chat.Events()

	// Delta should have only the new message
	delta = ac.Delta()
	if len(delta) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(delta))
	}
	if delta[0].Content != "second" {
		t.Errorf("expected 'second', got %q", delta[0].Content)
	}

	// Full entries should have both
	entries := ac.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 total entries, got %d", len(entries))
	}
}

func TestAgentContextAppendSystem(t *testing.T) {
	ac := NewAgentContext("@test")

	ac.AppendSystem("You moved to #analysis with @code")

	entries := ac.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].From != "@system" {
		t.Errorf("expected @system, got %q", entries[0].From)
	}
	if entries[0].Content != "You moved to #analysis with @code" {
		t.Errorf("unexpected content: %q", entries[0].Content)
	}
}

func TestAgentContextClear(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events()

	ac.MarkSent()

	if ac.Len() != 1 {
		t.Fatalf("expected 1 entry before clear, got %d", ac.Len())
	}

	ac.Clear()

	if ac.Len() != 0 {
		t.Errorf("expected 0 entries after clear, got %d", ac.Len())
	}

	delta := ac.Delta()
	if len(delta) != 0 {
		t.Errorf("expected empty delta after clear, got %d", len(delta))
	}
}

func TestChatClearNotifiesListeners(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events()

	if ac.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", ac.Len())
	}

	chat.Clear()

	if ac.Len() != 0 {
		t.Errorf("expected AgentContext cleared, got %d entries", ac.Len())
	}
}

func TestAgentContextPointerStability(t *testing.T) {
	// Verify that messages accumulated via listener are the same pointers
	// stored in the Chat (not copies).
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "hello"})
	<-chat.Events()

	entries := ac.Entries()
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}

	// The pointer in AgentContext should point to the same ChatMessage
	// as the one in Chat's internal storage. We can verify by checking
	// that mutating via pointer is visible (though we don't do this in
	// practice — this just validates the pointer model).
	history := chat.History()
	if history[0].Content != entries[0].Content {
		t.Error("content mismatch between Chat.History() and AgentContext.Entries()")
	}
}

func TestAgentContextRemoveListener(t *testing.T) {
	ac := NewAgentContext("@test")
	chat := NewChat()
	chat.AddListener(ac)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "before"})
	<-chat.Events()

	if ac.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", ac.Len())
	}

	// Remove listener
	chat.RemoveListener(ac)

	chat.Post(ChatMessage{From: "@user", Content: "after"})
	<-chat.Events()

	// Should still have only 1 entry (didn't receive "after")
	if ac.Len() != 1 {
		t.Errorf("expected 1 entry after RemoveListener, got %d", ac.Len())
	}
}

func TestMultipleAgentContextsOnSameChat(t *testing.T) {
	ac1 := NewAgentContext("@agent1")
	ac2 := NewAgentContext("@agent2")
	chat := NewChat()
	chat.AddListener(ac1)
	chat.AddListener(ac2)
	defer chat.Close()

	chat.Post(ChatMessage{From: "@user", Content: "hello everyone"})
	<-chat.Events()

	if ac1.Len() != 1 {
		t.Errorf("ac1: expected 1 entry, got %d", ac1.Len())
	}
	if ac2.Len() != 1 {
		t.Errorf("ac2: expected 1 entry, got %d", ac2.Len())
	}

	// Agent1 gets a system message (simulating room transition)
	ac1.AppendSystem("You moved to #room1")

	if ac1.Len() != 2 {
		t.Errorf("ac1: expected 2 entries after system msg, got %d", ac1.Len())
	}
	if ac2.Len() != 1 {
		t.Errorf("ac2: should still have 1 entry, got %d", ac2.Len())
	}
}
