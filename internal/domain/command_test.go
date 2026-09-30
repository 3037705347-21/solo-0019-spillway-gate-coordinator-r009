package domain

import (
	"testing"
	"time"
)

func TestExecutionGrantMatchesBinding(t *testing.T) {
	now := time.Now().UTC()
	grant, err := NewExecutionGrant("grant-1", "cmd-a", "gate-a", "token-xyz", now)
	if err != nil {
		t.Fatalf("new grant: %v", err)
	}

	if !grant.Matches("cmd-a", "gate-a", "token-xyz") {
		t.Fatal("grant should match the command and gate it was issued for")
	}
	if grant.Matches("cmd-b", "gate-b", "token-xyz") {
		t.Fatal("grant must not match a different command and gate")
	}
	if grant.Matches("cmd-b", "gate-a", "token-xyz") {
		t.Fatal("grant must not match a different command on the same gate")
	}
	if grant.Matches("cmd-a", "gate-b", "token-xyz") {
		t.Fatal("grant must not match the command on a different gate")
	}
	if grant.Matches("cmd-a", "gate-a", "wrong-token") {
		t.Fatal("grant must not match a different token")
	}
	if grant.Matches("", "gate-a", "token-xyz") || grant.Matches("cmd-a", "", "token-xyz") {
		t.Fatal("grant must not match an empty command or gate reference")
	}

	consumed := grant
	if err := consumed.Consume(now.Add(time.Minute)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if consumed.Matches("cmd-a", "gate-a", "token-xyz") {
		t.Fatal("consumed grant must no longer match anything")
	}
}
