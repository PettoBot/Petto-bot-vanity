package identity

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type Evaluation struct {
	GuildID       string
	UserID        string
	RoleID        string
	ActiveSources int
	Desired       bool
	Changed       bool
	OwnedByBot    bool
	ManualMarked  bool
	Error         error
}

type Engine struct {
	Store    Store
	Roles    RoleClient
	Logger   ActionLogger
	Notifier ActionLogger
	Now      func() time.Time
}

func (e *Engine) Evaluate(ctx context.Context, member MemberIdentity, vanityRules []VanityRule, guildTagRules []GuildTagRule) ([]Evaluation, error) {
	// Bot accounts are never targets of identity automation. Keep this guard in
	// the engine as defense in depth for Gateway events, manual syncs, and any
	// future caller that constructs a MemberIdentity directly.
	if member.IsBot {
		return nil, nil
	}
	if e.Store == nil || e.Roles == nil {
		return nil, fmt.Errorf("identity engine requires a store and role client")
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	roles := make(map[string]struct{})
	roleContexts := make(map[string]ActionEvent)
	matchingNotifications := make(map[string][]ActionEvent)
	pendingNotifications := make(map[string][]ActionEvent)
	for _, rule := range vanityRules {
		matched, err := MatchVanity(rule, member)
		if err != nil {
			matched = false
		}
		roles[rule.RoleID] = struct{}{}
		matchedValue := VanityValue(member, rule.Source)
		context := ActionEvent{
			GuildID: member.GuildID, UserID: member.UserID, RoleID: rule.RoleID, RuleID: rule.ID,
			RuleName: rule.Name, RuleCondition: string(rule.Comparison), MatchField: string(rule.Source), MatchedValue: matchedValue,
			Source: SourceVanity, Action: rule.Action, Value: rule.Word,
			Reason: fmt.Sprintf("No longer matched %s condition for value %s", rule.Source, matchedValue),
		}
		if matched {
			context.Reason = fmt.Sprintf("Matched %s condition for value %s", rule.Source, matchedValue)
		}
		if _, exists := roleContexts[rule.RoleID]; !exists || matched {
			roleContexts[rule.RoleID] = context
		}
		transition, err := e.recordGrant(ctx, RoleGrant{
			GuildID: member.GuildID, UserID: member.UserID, RoleID: rule.RoleID, RuleID: rule.ID,
			SourceType: SourceVanity, Action: rule.Action, Matched: matched, LastEvaluatedAt: now().UTC(),
		}, member, rule.ID, SourceVanity, matched, rule.Word, err)
		if err != nil {
			return nil, err
		}
		if matched && rule.Action == ActionAddRole {
			matchingNotifications[rule.RoleID] = append(matchingNotifications[rule.RoleID], context)
			if !transition.HadPrevious || !transition.PreviousMatched {
				pendingNotifications[rule.RoleID] = append(pendingNotifications[rule.RoleID], context)
			}
		}
	}
	for _, rule := range guildTagRules {
		matched := MatchGuildTag(rule, member.PrimaryGuild)
		roles[rule.RoleID] = struct{}{}
		tag, tagGuildID, tagEnabled, tagBadge := "", "", "", ""
		if member.PrimaryGuild != nil {
			tag, tagGuildID, tagBadge = member.PrimaryGuild.Tag, member.PrimaryGuild.IdentityGuildID, member.PrimaryGuild.Badge
			if member.PrimaryGuild.IdentityEnabled != nil {
				tagEnabled = strconv.FormatBool(*member.PrimaryGuild.IdentityEnabled)
			}
		}
		context := ActionEvent{
			GuildID: member.GuildID, UserID: member.UserID, RoleID: rule.RoleID, RuleID: rule.ID,
			RuleName: rule.Name, RuleCondition: string(rule.Condition), MatchedValue: rule.Value,
			Tag: tag, TagGuildID: tagGuildID, TagEnabled: tagEnabled, TagBadge: tagBadge,
			Source: SourceGuildTag, Action: rule.Action, Value: rule.Value,
			Reason: fmt.Sprintf("No longer matched %s condition for value %s", rule.Condition, rule.Value),
		}
		if matched {
			context.Reason = fmt.Sprintf("Matched %s condition for value %s", rule.Condition, rule.Value)
		}
		if _, exists := roleContexts[rule.RoleID]; !exists || matched {
			roleContexts[rule.RoleID] = context
		}
		transition, err := e.recordGrant(ctx, RoleGrant{
			GuildID: member.GuildID, UserID: member.UserID, RoleID: rule.RoleID, RuleID: rule.ID,
			SourceType: SourceGuildTag, Action: rule.Action, Matched: matched, LastEvaluatedAt: now().UTC(),
		}, member, rule.ID, SourceGuildTag, matched, rule.Value, nil)
		if err != nil {
			return nil, err
		}
		if matched && rule.Action == ActionAddRole {
			matchingNotifications[rule.RoleID] = append(matchingNotifications[rule.RoleID], context)
			if !transition.HadPrevious || !transition.PreviousMatched {
				pendingNotifications[rule.RoleID] = append(pendingNotifications[rule.RoleID], context)
			}
		}
	}

	roleIDs := make([]string, 0, len(roles))
	for roleID := range roles {
		if roleID != "" {
			roleIDs = append(roleIDs, roleID)
		}
	}
	sort.Strings(roleIDs)
	results := make([]Evaluation, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		present := false
		if member.RoleIDs != nil {
			_, present = member.RoleIDs[roleID]
		}
		if err := e.Store.ObserveRolePresence(ctx, member.GuildID, member.UserID, roleID, present); err != nil {
			return nil, err
		}
		active, err := e.Store.ActiveRoleSources(ctx, member.GuildID, member.UserID, roleID)
		if err != nil {
			return nil, err
		}
		state, err := e.Store.GetRoleState(ctx, member.GuildID, member.UserID, roleID)
		if err != nil {
			return nil, err
		}
		result := Evaluation{
			GuildID: member.GuildID, UserID: member.UserID, RoleID: roleID,
			ActiveSources: active, Desired: active > 0, OwnedByBot: state.BotAddedRole,
			ManualMarked: state.ManualMarked,
		}
		switch {
		case active > 0 && !present:
			result.Changed = true
			audit := roleAudit(member, roleID, ActionAddRole, "requested", "role-add")
			if err := e.Store.RecordAudit(ctx, audit); err != nil {
				return nil, err
			}
			if err := e.Roles.AddRole(ctx, member.GuildID, member.UserID, roleID); err != nil {
				result.Error = err
				_ = e.Store.RecordAudit(ctx, roleAuditWithError(audit, err))
				if e.Logger != nil {
					action := roleContexts[roleID]
					action.Action, action.Result, action.Error = ActionAddRole, "error", err
					_ = e.Logger.EmitAction(ctx, action)
				}
			} else {
				if err := e.Store.MarkBotAdded(ctx, member.GuildID, member.UserID, roleID); err != nil {
					return nil, err
				}
				_ = e.Store.RecordAudit(ctx, roleAudit(member, roleID, ActionAddRole, "completed", "role-add-completed"))
				if e.Logger != nil {
					action := roleContexts[roleID]
					action.Action, action.Result = ActionAddRole, "completed"
					_ = e.Logger.EmitAction(ctx, action)
				}
				e.emitNotifications(ctx, matchingNotifications[roleID])
			}
		case active > 0 && present:
			e.emitNotifications(ctx, pendingNotifications[roleID])
		case active == 0 && present && state.BotAddedRole && !state.ManualMarked:
			result.Changed = true
			audit := roleAudit(member, roleID, ActionRemoveRole, "requested", "role-remove")
			if err := e.Store.RecordAudit(ctx, audit); err != nil {
				return nil, err
			}
			if err := e.Roles.RemoveRole(ctx, member.GuildID, member.UserID, roleID); err != nil {
				result.Error = err
				_ = e.Store.RecordAudit(ctx, roleAuditWithError(audit, err))
				if e.Logger != nil {
					action := roleContexts[roleID]
					action.Action, action.Result, action.Error = ActionRemoveRole, "error", err
					_ = e.Logger.EmitAction(ctx, action)
				}
			} else {
				if err := e.Store.MarkBotRemoved(ctx, member.GuildID, member.UserID, roleID); err != nil {
					return nil, err
				}
				_ = e.Store.RecordAudit(ctx, roleAudit(member, roleID, ActionRemoveRole, "completed", "role-remove-completed"))
				if e.Logger != nil {
					action := roleContexts[roleID]
					action.Action, action.Result = ActionRemoveRole, "completed"
					_ = e.Logger.EmitAction(ctx, action)
				}
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (e *Engine) recordGrant(ctx context.Context, grant RoleGrant, member MemberIdentity, ruleID string, source Source, matched bool, value string, matchErr error) (GrantTransition, error) {
	metadata := map[string]string{"matched": fmt.Sprintf("%t", matched), "value": value}
	if matchErr != nil {
		metadata["evaluation_error"] = matchErr.Error()
	}
	return e.Store.RecordGrant(ctx, grant, AuditIntent{
		EventType: "identity_role_intent", DedupeKey: fmt.Sprintf("grant:%s:%s:%s:%s:%t", member.GuildID, member.UserID, grant.RoleID, ruleID, matched),
		GuildID: member.GuildID, UserID: member.UserID, RoleID: grant.RoleID, RuleID: ruleID,
		Source: source, Action: grant.Action, Result: "evaluated", Metadata: metadata,
	})
}

func (e *Engine) emitNotifications(ctx context.Context, actions []ActionEvent) {
	if e.Notifier == nil {
		return
	}
	for _, action := range actions {
		action.Action, action.Result, action.Error = ActionAddRole, "completed", nil
		_ = e.Notifier.EmitAction(ctx, action)
	}
}

func roleAudit(member MemberIdentity, roleID string, action Action, result, suffix string) AuditIntent {
	return AuditIntent{
		EventType: "identity_role_" + string(action),
		DedupeKey: fmt.Sprintf("%s:%s:%s:%s", suffix, member.GuildID, member.UserID, roleID),
		GuildID:   member.GuildID, UserID: member.UserID, RoleID: roleID, Action: action, Result: result,
		Metadata: map[string]string{"source": "shared-role-ledger"},
	}
}

func roleAuditWithError(audit AuditIntent, err error) AuditIntent {
	audit.DedupeKey += ":error"
	audit.Result = "error"
	audit.Error = err.Error()
	return audit
}
