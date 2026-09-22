package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Source string

const (
	SourceVanity   Source = "vanity"
	SourceGuildTag Source = "guildtag"
)

type Comparison string

const (
	ComparisonEquals     Comparison = "equals"
	ComparisonContains   Comparison = "contains"
	ComparisonStartsWith Comparison = "starts_with"
	ComparisonEndsWith   Comparison = "ends_with"
	ComparisonRegex      Comparison = "regex"
)

type VanitySource string

const (
	VanityUsername      VanitySource = "username"
	VanityGlobalName    VanitySource = "global_name"
	VanityGuildNickname VanitySource = "guild_nickname"
	VanityDisplayName   VanitySource = "display_name"
	VanityCustomStatus  VanitySource = "custom_status"
)

type Action string

const (
	ActionAddRole    Action = "add_role"
	ActionRemoveRole Action = "remove_role"
)

type GuildTagCondition string

const (
	ConditionIsGuildID        GuildTagCondition = "is_guild_id"
	ConditionIsNotGuildID     GuildTagCondition = "is_not_guild_id"
	ConditionIdentityEnabled  GuildTagCondition = "identity_enabled"
	ConditionIdentityDisabled GuildTagCondition = "identity_disabled"
	ConditionTagEquals        GuildTagCondition = "tag_equals"
	ConditionTagNotEquals     GuildTagCondition = "tag_not_equals"
)

type Normalization struct {
	CaseFold      bool `json:"case_fold"`
	TrimSpace     bool `json:"trim_space"`
	CollapseSpace bool `json:"collapse_space"`
}

type VanityRule struct {
	ID            string
	GuildID       string
	Name          string
	Word          string
	Source        VanitySource
	Comparison    Comparison
	RoleID        string
	Action        Action
	Enabled       bool
	Priority      int
	Normalization Normalization
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type GuildTagRule struct {
	ID        string
	GuildID   string
	Name      string
	Condition GuildTagCondition
	Value     string
	RoleID    string
	Action    Action
	Enabled   bool
	Priority  int
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PrimaryGuild struct {
	IdentityGuildID string `json:"identity_guild_id"`
	IdentityEnabled *bool  `json:"identity_enabled"`
	Tag             string `json:"tag"`
	Badge           string `json:"badge"`
}

type MemberIdentity struct {
	GuildID              string
	UserID               string
	IsBot                bool
	Username             string
	GlobalName           string
	GuildNickname        string
	DisplayName          string
	AvatarURL            string
	CustomStatus         string
	UnknownVanitySources map[VanitySource]struct{}
	PrimaryGuild         *PrimaryGuild
	RoleIDs              map[string]struct{}
}

type RoleGrant struct {
	GuildID         string
	UserID          string
	RoleID          string
	RuleID          string
	SourceType      Source
	Action          Action
	Matched         bool
	BotAddedRole    bool
	LastEvaluatedAt time.Time
}

// GrantTransition describes whether a rule was already matching before the
// current evaluation. It lets notifications fire when a rule starts matching
// even if its shared role was already present on the member.
type GrantTransition struct {
	HadPrevious     bool
	PreviousMatched bool
}

type RoleState struct {
	GuildID          string
	UserID           string
	RoleID           string
	BotAddedRole     bool
	ManualMarked     bool
	LastKnownPresent bool
}

type AuditIntent struct {
	EventType string
	DedupeKey string
	GuildID   string
	ActorID   string
	UserID    string
	RoleID    string
	RuleID    string
	Source    Source
	Action    Action
	Result    string
	Error     string
	Metadata  map[string]string
}

type ActionEvent struct {
	GuildID         string
	UserID          string
	UserName        string
	UserDisplayName string
	UserAvatar      string
	RoleID          string
	RuleID          string
	RuleName        string
	RuleCondition   string
	MatchField      string
	MatchedValue    string
	Tag             string
	TagGuildID      string
	TagEnabled      string
	TagBadge        string
	Source          Source
	Action          Action
	Value           string
	Reason          string
	Result          string
	Error           error
}

type ActionLogger interface {
	EmitAction(context.Context, ActionEvent) error
}

type GrantScope struct {
	RuleID string
	RoleID string
	Action Action
}

type Store interface {
	ListVanityRules(context.Context, string) ([]VanityRule, error)
	ListGuildTagRules(context.Context, string) ([]GuildTagRule, error)
	InvalidateStaleGrants(context.Context, string, string, Source, []GrantScope) error
	ManagedRoleIDs(context.Context, string, string) ([]string, error)
	RecordGrant(context.Context, RoleGrant, AuditIntent) (GrantTransition, error)
	ActiveRoleSources(context.Context, string, string, string) (int, error)
	ActiveRoleRemovals(context.Context, string, string, string) (int, error)
	GetRoleState(context.Context, string, string, string) (RoleState, error)
	ObserveRolePresence(context.Context, string, string, string, bool) error
	MarkBotAdded(context.Context, string, string, string) error
	MarkBotRemoved(context.Context, string, string, string) error
	RecordAudit(context.Context, AuditIntent) error
}

type RoleClient interface {
	AddRole(context.Context, string, string, string) error
	RemoveRole(context.Context, string, string, string) error
}

func NewID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}
