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

func TestRemoveRuleWinsOverMatchingAddRule(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	add := VanityRule{ID: "add", GuildID: "guild", Name: "add", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionAddRole, Enabled: true}
	remove := VanityRule{ID: "remove", GuildID: "guild", Name: "remove", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionRemoveRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{}}

	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{add}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 1 {
		t.Fatalf("expected initial bot-owned add, got %#v", roles.added)
	}
	member.RoleIDs["role"] = struct{}{}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{add, remove}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("matching remove_role must override add_role, got removals %#v", roles.removed)
	}
}

func TestRemoveRuleNeverRemovesManualRole(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	remove := VanityRule{ID: "remove", GuildID: "guild", Name: "remove", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionRemoveRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "manual", Username: "rep", RoleIDs: map[string]struct{}{"role": {}}}

	results, err := engine.Evaluate(context.Background(), member, []VanityRule{remove}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("manual role was removed by remove_role: %#v", roles.removed)
	}
	if len(results) != 1 || !results[0].ManualMarked {
		t.Fatalf("manual ownership was not preserved: %#v", results)
	}
}

func TestDeletedRuleInvalidatesGrantAndRemovesBotOwnedRole(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	add := VanityRule{ID: "old", GuildID: "guild", Name: "old", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionAddRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{}}

	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{add}, nil); err != nil {
		t.Fatal(err)
	}
	member.RoleIDs["role"] = struct{}{}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("stale bot-owned role was not removed after rule deletion: %#v", roles.removed)
	}
	state, err := ledger.GetRoleState(context.Background(), "guild", "user", "role")
	if err != nil {
		t.Fatal(err)
	}
	if state.BotAddedRole {
		t.Fatal("bot ownership must be cleared after successful removal")
	}
}

func TestEditedRuleInvalidatesOldRoleScope(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	oldRule := VanityRule{ID: "same-id", GuildID: "guild", Name: "rule", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "old-role", Action: ActionAddRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{}}

	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{oldRule}, nil); err != nil {
		t.Fatal(err)
	}
	member.RoleIDs["old-role"] = struct{}{}
	edited := oldRule
	edited.RoleID = "new-role"
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{edited}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 || roles.removed[0] != "old-role" {
		t.Fatalf("edited rule left old role managed: %#v", roles.removed)
	}
	if len(roles.added) != 2 || roles.added[1] != "new-role" {
		t.Fatalf("edited rule did not add new role: %#v", roles.added)
	}
}

func TestDisabledRuleInvalidatesExistingGrant(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	rule := VanityRule{ID: "rule", GuildID: "guild", Name: "rule", Word: "rep", Source: VanityUsername, Comparison: ComparisonContains, RoleID: "role", Action: ActionAddRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "user", Username: "rep", RoleIDs: map[string]struct{}{}}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{rule}, nil); err != nil {
		t.Fatal(err)
	}
	member.RoleIDs["role"] = struct{}{}
	rule.Enabled = false
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{rule}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("disabled rule left its bot-owned role behind: %#v", roles.removed)
	}
}

func TestLegacyRemovedOwnershipDoesNotConsumeManualReAdd(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}

	ledger.mu.Lock()
	ledger.states[stateKey("guild", "user", "role")] = RoleState{
		GuildID: "guild", UserID: "user", RoleID: "role",
		BotAddedRole: true, LastKnownPresent: false,
	}
	ledger.mu.Unlock()

	member := MemberIdentity{GuildID: "guild", UserID: "user", RoleIDs: map[string]struct{}{"role": {}}}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{}, []GuildTagRule{}); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("legacy ownership caused a manual re-add to be removed: %#v", roles.removed)
	}
	state, err := ledger.GetRoleState(context.Background(), "guild", "user", "role")
	if err != nil {
		t.Fatal(err)
	}
	if state.BotAddedRole || !state.ManualMarked {
		t.Fatalf("legacy ownership was not repaired as manual: %#v", state)
	}
}

func TestSkippedGuildTagEvaluationPreservesManagedGrant(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	enabled := true
	tags := []GuildTagRule{{
		ID:        "tag-1",
		GuildID:   "guild",
		Name:      "server-tag",
		Condition: ConditionIsGuildID,
		Value:     "source",
		RoleID:    "role",
		Action:    ActionAddRole,
		Enabled:   true,
	}}
	member := MemberIdentity{
		GuildID:      "guild",
		UserID:       "user",
		PrimaryGuild: &PrimaryGuild{IdentityGuildID: "source", IdentityEnabled: &enabled},
		RoleIDs:      map[string]struct{}{},
	}

	if _, err := engine.Evaluate(context.Background(), member, nil, tags); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 1 {
		t.Fatalf("expected role to be added once, got %#v", roles.added)
	}

	// Simulate a later event where Discord did not return authoritative
	// primary_guild data. Passing nil tagRules means the Guild Tag source was
	// intentionally skipped and must not invalidate the previous matched grant.
	member.PrimaryGuild = nil
	member.RoleIDs = map[string]struct{}{"role": {}}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("unknown primary_guild must not revoke a managed Guild Tag role: %#v", roles.removed)
	}
	active, err := ledger.ActiveRoleSources(context.Background(), "guild", "user", "role")
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("expected previous Guild Tag grant to remain active, got %d", active)
	}
}

func TestUnknownCustomStatusPreservesPreviousGrant(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	rule := VanityRule{ID: "status-rule", GuildID: "guild", Name: "status", Word: "petto", Source: VanityCustomStatus, Comparison: ComparisonContains, RoleID: "role", Action: ActionAddRole, Enabled: true}
	member := MemberIdentity{GuildID: "guild", UserID: "user", CustomStatus: "petto forever", RoleIDs: map[string]struct{}{}}

	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{rule}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 1 {
		t.Fatalf("expected initial role add, got %#v", roles.added)
	}

	member.RoleIDs["role"] = struct{}{}
	member.CustomStatus = ""
	member.UnknownVanitySources = map[VanitySource]struct{}{VanityCustomStatus: {}}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{rule}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 0 {
		t.Fatalf("unknown custom status removed a previously justified role: %#v", roles.removed)
	}

	member.UnknownVanitySources = nil
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{rule}, nil); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("known empty custom status should remove the bot-owned role, got %#v", roles.removed)
	}
}

func TestKnownNoPrimaryGuildRevokesManagedGuildTagGrant(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	enabled := true
	rules := []GuildTagRule{{
		ID: "tag-known-null", GuildID: "guild", Name: "tag", Condition: ConditionIsGuildID,
		Value: "source", RoleID: "role", Action: ActionAddRole, Enabled: true,
	}}
	member := MemberIdentity{
		GuildID: "guild", UserID: "user", RoleIDs: map[string]struct{}{},
		PrimaryGuild: &PrimaryGuild{IdentityGuildID: "source", IdentityEnabled: &enabled},
	}
	if _, err := engine.Evaluate(context.Background(), member, nil, rules); err != nil {
		t.Fatal(err)
	}
	if len(roles.added) != 1 {
		t.Fatalf("expected initial role add, got %#v", roles.added)
	}

	// Explicit primary_guild:null is represented by a known evaluation with a
	// nil PrimaryGuild. The caller passes a non-nil tag rule slice so stale
	// grants are invalidated. This must revoke a role Petto previously added.
	member.PrimaryGuild = nil
	member.RoleIDs = map[string]struct{}{"role": {}}
	if _, err := engine.Evaluate(context.Background(), member, nil, rules); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("known no-primary-guild must remove stale bot role, got %#v", roles.removed)
	}
}

func TestHistoricalGrantOwnershipRecoversMissingRoleState(t *testing.T) {
	ledger := NewMemoryLedger()
	roles := &fakeRoles{}
	engine := Engine{Store: ledger, Roles: roles}
	ledger.mu.Lock()
	ledger.grants[grantKey("guild", "user", "role", "old-rule")] = RoleGrant{
		GuildID: "guild", UserID: "user", RoleID: "role", RuleID: "old-rule",
		SourceType: SourceGuildTag, Action: ActionAddRole, Matched: false, BotAddedRole: true,
	}
	ledger.mu.Unlock()

	member := MemberIdentity{GuildID: "guild", UserID: "user", RoleIDs: map[string]struct{}{"role": {}}}
	if _, err := engine.Evaluate(context.Background(), member, []VanityRule{}, []GuildTagRule{}); err != nil {
		t.Fatal(err)
	}
	if len(roles.removed) != 1 {
		t.Fatalf("historical bot ownership should recover cleanup eligibility, got %#v", roles.removed)
	}
}
