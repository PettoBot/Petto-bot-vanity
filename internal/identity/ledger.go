package identity

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryLedger is intentionally small but complete enough for deterministic
// unit tests of the shared-role safety rules.
type MemoryLedger struct {
	mu     sync.Mutex
	grants map[string]RoleGrant
	states map[string]RoleState
	audits []AuditIntent
	dedupe map[string]struct{}
}

func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{
		grants: make(map[string]RoleGrant),
		states: make(map[string]RoleState),
		dedupe: make(map[string]struct{}),
	}
}

func (m *MemoryLedger) ListVanityRules(context.Context, string) ([]VanityRule, error) {
	return nil, nil
}
func (m *MemoryLedger) ListGuildTagRules(context.Context, string) ([]GuildTagRule, error) {
	return nil, nil
}

func (m *MemoryLedger) InvalidateStaleGrants(_ context.Context, guildID, userID string, source Source, active []GrantScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, grant := range m.grants {
		if grant.GuildID != guildID || grant.UserID != userID || grant.SourceType != source || !grant.Matched {
			continue
		}
		valid := false
		for _, scope := range active {
			if grant.RuleID == scope.RuleID && grant.RoleID == scope.RoleID && grant.Action == scope.Action {
				valid = true
				break
			}
		}
		if !valid {
			grant.Matched = false
			m.grants[key] = grant
		}
	}
	return nil
}

func (m *MemoryLedger) ManagedRoleIDs(_ context.Context, guildID, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	roles := make(map[string]struct{})
	for _, state := range m.states {
		if state.GuildID == guildID && state.UserID == userID && state.BotAddedRole && state.RoleID != "" {
			roles[state.RoleID] = struct{}{}
		}
	}
	for _, grant := range m.grants {
		if grant.GuildID == guildID && grant.UserID == userID && grant.Matched && grant.RoleID != "" {
			roles[grant.RoleID] = struct{}{}
		}
	}
	result := make([]string, 0, len(roles))
	for roleID := range roles {
		result = append(result, roleID)
	}
	return result, nil
}

func (m *MemoryLedger) RecordGrant(_ context.Context, grant RoleGrant, audit AuditIntent) (GrantTransition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := grantKey(grant.GuildID, grant.UserID, grant.RoleID, grant.RuleID)
	transition := GrantTransition{}
	if previous, ok := m.grants[key]; ok {
		transition.HadPrevious = true
		transition.PreviousMatched = previous.Matched
		grant.BotAddedRole = previous.BotAddedRole
	}
	m.grants[key] = grant
	m.recordAuditLocked(audit)
	return transition, nil
}

func (m *MemoryLedger) ActiveRoleSources(_ context.Context, guildID, userID, roleID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, grant := range m.grants {
		if grant.GuildID == guildID && grant.UserID == userID && grant.RoleID == roleID && grant.Matched && grant.Action == ActionAddRole {
			count++
		}
	}
	return count, nil
}

func (m *MemoryLedger) ActiveRoleRemovals(_ context.Context, guildID, userID, roleID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, grant := range m.grants {
		if grant.GuildID == guildID && grant.UserID == userID && grant.RoleID == roleID && grant.Matched && grant.Action == ActionRemoveRole {
			count++
		}
	}
	return count, nil
}

func (m *MemoryLedger) GetRoleState(_ context.Context, guildID, userID, roleID string) (RoleState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[stateKey(guildID, userID, roleID)]
	state.GuildID, state.UserID, state.RoleID = guildID, userID, roleID
	return state, nil
}

func (m *MemoryLedger) ObserveRolePresence(_ context.Context, guildID, userID, roleID string, present bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := stateKey(guildID, userID, roleID)
	state := m.states[key]
	if present && (!state.BotAddedRole || !state.LastKnownPresent) {
		// A present role that Petto does not currently own is manual. The
		// lastKnownPresent=false case also repairs ownership left behind by
		// older versions whose MarkBotRemoved did not clear bot_added_role.
		state.ManualMarked = true
		state.BotAddedRole = false
	}
	state.GuildID, state.UserID, state.RoleID = guildID, userID, roleID
	state.LastKnownPresent = present
	m.states[key] = state
	return nil
}

func (m *MemoryLedger) MarkBotAdded(_ context.Context, guildID, userID, roleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := stateKey(guildID, userID, roleID)
	state := m.states[key]
	state.GuildID, state.UserID, state.RoleID = guildID, userID, roleID
	state.BotAddedRole = true
	state.ManualMarked = false
	state.LastKnownPresent = true
	m.states[key] = state
	return nil
}

func (m *MemoryLedger) MarkBotRemoved(_ context.Context, guildID, userID, roleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := stateKey(guildID, userID, roleID)
	state := m.states[key]
	state.GuildID, state.UserID, state.RoleID = guildID, userID, roleID
	state.BotAddedRole = false
	state.ManualMarked = false
	state.LastKnownPresent = false
	m.states[key] = state
	return nil
}

func (m *MemoryLedger) RecordAudit(_ context.Context, audit AuditIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordAuditLocked(audit)
	return nil
}

func (m *MemoryLedger) recordAuditLocked(audit AuditIntent) {
	if audit.DedupeKey != "" {
		if _, ok := m.dedupe[audit.DedupeKey]; ok {
			return
		}
		m.dedupe[audit.DedupeKey] = struct{}{}
	}
	m.audits = append(m.audits, audit)
}

func (m *MemoryLedger) Audits() []AuditIntent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]AuditIntent, len(m.audits))
	copy(result, m.audits)
	return result
}

func grantKey(guildID, userID, roleID, ruleID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", guildID, userID, roleID, ruleID)
}

func stateKey(guildID, userID, roleID string) string {
	return fmt.Sprintf("%s:%s:%s", guildID, userID, roleID)
}

func nowOrUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
