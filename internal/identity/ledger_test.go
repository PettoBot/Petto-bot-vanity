package identity

import (
	"context"
	"errors"
	"testing"
)

type fakeRoles struct {
	added, removed    []string
	addErr, removeErr error
}

type captureActionLogger struct{ events []ActionEvent }

func (c *captureActionLogger) EmitAction(_ context.Context, event ActionEvent) error {
	c.events = append(c.events, event)
	return nil
}

func (f *fakeRoles) AddRole(_ context.Context, _, _, roleID string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, roleID)
	return nil
}
func (f *fakeRoles) RemoveRole(_ context.Context, _, _, roleID string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, roleID)
	return nil
}

func sharedRules() ([]VanityRule, []GuildTagRule) {
	return []VanityRule{{ID: "vanity-1", GuildID: "guild", Name: "rep-word", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionAddRole, Enabled: true, Normalization: Normalization{CaseFold: true, TrimSpace: true}}}, []GuildTagRule{{ID: "tag-1", GuildID: "guild", Name: "server-tag", Condition: ConditionIsGuildID, Value: "source", RoleID: "role", Action: ActionAddRole, Enabled: true}}
}

func TestSharedRoleIsPreservedWhileAnotherSourceMatches(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	vanity, tags := sharedRules()
	enabled := true
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", PrimaryGuild: &PrimaryGuild{IdentityGuildID: "source", IdentityEnabled: &enabled}, RoleIDs: map[string]struct{}{}}
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 1 {
		t.Fatalf("expected one add, got %d", len(roles.added))
	}
	member.RoleIDs["role"] = struct{}{}
	member.Username = "ordinary"
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("role removed despite Guild Tag source: %#v", roles.removed)
	}
	member.Username = "rep"
	member.PrimaryGuild = &PrimaryGuild{IdentityGuildID: "other", IdentityEnabled: &enabled}
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("role removed despite Vanity source: %#v", roles.removed)
	}
	member.Username = "ordinary"
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("expected one removal after both sources disappeared, got %d", len(roles.removed))
	}
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("retry must be idempotent, got %d removals", len(roles.removed))
	}
}

func TestManualRoleIsNeverRemoved(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	vanity, tags := sharedRules()
	enabled := true
	member := MemberIdentity{GuildID: "guild", UserID: "manual", Username: "ordinary", PrimaryGuild: &PrimaryGuild{IdentityGuildID: "other", IdentityEnabled: &enabled}, RoleIDs: map[string]struct{}{"role": {}}}
	if _, err := engine.Evaluate(context.Background(), member, vanity, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatal("manual role was removed")
	}
}

func TestRoleMutationFailureIsRecoverable(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{addErr: errors.New("discord unavailable")}
	engine := Engine{Store: ledger, Roles: roles}
	vanity, tags := sharedRules()
	enabled := true
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", PrimaryGuild: &PrimaryGuild{IdentityGuildID: "other", IdentityEnabled: &enabled}}
	results, err := engine.Evaluate(context.Background(), member, vanity, tags)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Error == nil {
		t.Fatal("Discord failure was not returned as a recoverable evaluation result")
	}
}

func TestNotifierOnlyReceivesSuccessfulRoleAdds(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	notifier := &captureActionLogger{}
	engine := Engine{Store: ledger, Roles: roles, Notifier: notifier}
	vanity, _ := sharedRules()
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{}}
	if _, err := engine.Evaluate(context.Background(), member, vanity, nil); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0].Action != ActionAddRole {
		t.Fatalf("expected one successful add notification, got %#v", notifier.events)
	}
	member.RoleIDs["role"] = struct{}{}
	if _, err := engine.Evaluate(context.Background(), member, vanity, nil); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("same matching rule must not notify repeatedly: %#v", notifier.events)
	}
	member.Username = "ordinary"
	member.RoleIDs["role"] = struct{}{}
	if _, err := engine.Evaluate(context.Background(), member, vanity, nil); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("role removal must not send a user notification: %#v", notifier.events)
	}
}

func TestNotifierRunsWhenMatchingRoleAlreadyExists(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	notifier := &captureActionLogger{}
	engine := Engine{Store: ledger, Roles: roles, Notifier: notifier}
	vanity, _ := sharedRules()
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{"role": {}}}
	if _, err := engine.Evaluate(context.Background(), member, vanity, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 0 {
		t.Fatalf("existing role must not be added again: %#v", roles.added)
	}
	if len(notifier.events) != 1 || notifier.events[0].Action != ActionAddRole {
		t.Fatalf("expected notification for an existing matching role, got %#v", notifier.events)
	}
}

func TestBotMembersAreIgnored(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	notifier := &captureActionLogger{}
	engine := Engine{Store: ledger, Roles: roles, Notifier: notifier}
	vanity, _ := sharedRules()
	member := MemberIdentity{GuildID: "guild", UserID: "bot", IsBot: true, Username: "rep", RoleIDs: map[string]struct{}{}}

	results, err := engine.Evaluate(context.Background(), member, vanity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || len(roles.added) != 0 || len(notifier.events) != 0 {
		t.Fatalf("bot member must be ignored, got results=%#v added=%#v notifications=%#v", results, roles.added, notifier.events)
	}
}
