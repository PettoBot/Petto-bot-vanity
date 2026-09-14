package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) ListVanityRules(ctx context.Context, guildID string) ([]identity.VanityRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,guild_id,name,word,source,comparison,role_id,action,enabled,priority,
		       normalization,created_by,created_at,updated_at
		FROM vanity_rules WHERE guild_id=$1 AND enabled=true AND deleted_at IS NULL
		ORDER BY priority DESC, created_at ASC`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]identity.VanityRule, 0)
	for rows.Next() {
		var rule identity.VanityRule
		var normalization []byte
		if err := rows.Scan(&rule.ID, &rule.GuildID, &rule.Name, &rule.Word, &rule.Source, &rule.Comparison,
			&rule.RoleID, &rule.Action, &rule.Enabled, &rule.Priority, &normalization, &rule.CreatedBy,
			&rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(normalization, &rule.Normalization); err != nil {
			return nil, fmt.Errorf("decode vanity normalization: %w", err)
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (s *Store) ListGuildTagRules(ctx context.Context, guildID string) ([]identity.GuildTagRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,guild_id,name,condition,value,role_id,action,enabled,priority,created_by,created_at,updated_at
		FROM guildtag_rules WHERE guild_id=$1 AND enabled=true AND deleted_at IS NULL
		ORDER BY priority DESC, created_at ASC`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]identity.GuildTagRule, 0)
	for rows.Next() {
		var rule identity.GuildTagRule
		if err := rows.Scan(&rule.ID, &rule.GuildID, &rule.Name, &rule.Condition, &rule.Value, &rule.RoleID,
			&rule.Action, &rule.Enabled, &rule.Priority, &rule.CreatedBy, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (s *Store) HasActiveRules(ctx context.Context, guildID string) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM vanity_rules WHERE guild_id=$1 AND enabled=true AND deleted_at IS NULL
			UNION ALL
			SELECT 1 FROM guildtag_rules WHERE guild_id=$1 AND enabled=true AND deleted_at IS NULL
			UNION ALL
			SELECT 1 FROM identity_role_state WHERE guild_id=$1 AND bot_added_role=true
			UNION ALL
			SELECT 1 FROM identity_role_grants WHERE guild_id=$1 AND matched=true
		)`, guildID).Scan(&found)
	return found, err
}

func (s *Store) InvalidateStaleGrants(ctx context.Context, guildID, userID string, source identity.Source, active []identity.GrantScope) error {
	args := []any{guildID, userID, source}
	query := `
		UPDATE identity_role_grants
		SET matched=false,last_evaluated_at=now(),updated_at=now()
		WHERE guild_id=$1 AND user_id=$2 AND source_type=$3 AND matched=true`
	if len(active) > 0 {
		clauses := make([]string, 0, len(active))
		for _, scope := range active {
			base := len(args) + 1
			clauses = append(clauses, fmt.Sprintf("(rule_id=$%d AND role_id=$%d AND action=$%d)", base, base+1, base+2))
			args = append(args, scope.RuleID, scope.RoleID, scope.Action)
		}
		query += " AND NOT (" + strings.Join(clauses, " OR ") + ")"
	}
	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *Store) ManagedRoleIDs(ctx context.Context, guildID, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role_id FROM identity_role_state
		WHERE guild_id=$1 AND user_id=$2 AND bot_added_role=true
		UNION
		SELECT role_id FROM identity_role_grants
		WHERE guild_id=$1 AND user_id=$2 AND (matched=true OR bot_added_role=true)`, guildID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var roleID string
		if err := rows.Scan(&roleID); err != nil {
			return nil, err
		}
		if roleID != "" {
			result = append(result, roleID)
		}
	}
	return result, rows.Err()
}

func (s *Store) RecordGrant(ctx context.Context, grant identity.RoleGrant, audit identity.AuditIntent) (identity.GrantTransition, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return identity.GrantTransition{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var transition identity.GrantTransition
	var previousMatched bool
	selectErr := tx.QueryRow(ctx, `
		SELECT matched FROM identity_role_grants
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND rule_id=$4
		FOR UPDATE`, grant.GuildID, grant.UserID, grant.RoleID, grant.RuleID).Scan(&previousMatched)
	if selectErr == nil {
		transition.HadPrevious = true
		transition.PreviousMatched = previousMatched
	} else if !errors.Is(selectErr, pgx.ErrNoRows) {
		return transition, selectErr
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO identity_role_grants
			(guild_id,user_id,role_id,rule_id,source_type,action,matched,bot_added_role,last_evaluated_at,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),now())
		ON CONFLICT (guild_id,user_id,role_id,rule_id) DO UPDATE SET
			source_type=EXCLUDED.source_type, action=EXCLUDED.action, matched=EXCLUDED.matched,
			bot_added_role=identity_role_grants.bot_added_role,
			last_evaluated_at=EXCLUDED.last_evaluated_at, updated_at=now()`,
		grant.GuildID, grant.UserID, grant.RoleID, grant.RuleID, grant.SourceType, grant.Action, grant.Matched,
		grant.BotAddedRole, grant.LastEvaluatedAt)
	if err != nil {
		return transition, fmt.Errorf("record role grant: %w", err)
	}
	if err := insertAudit(ctx, tx, audit); err != nil {
		return transition, err
	}
	if err := tx.Commit(ctx); err != nil {
		return transition, err
	}
	return transition, nil
}

func (s *Store) ActiveRoleSources(ctx context.Context, guildID, userID, roleID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity_role_grants
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND matched=true AND action=$4`,
		guildID, userID, roleID, identity.ActionAddRole).Scan(&count)
	return count, err
}

func (s *Store) ActiveRoleRemovals(ctx context.Context, guildID, userID, roleID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity_role_grants
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND matched=true AND action=$4`,
		guildID, userID, roleID, identity.ActionRemoveRole).Scan(&count)
	return count, err
}

func (s *Store) GetRoleState(ctx context.Context, guildID, userID, roleID string) (identity.RoleState, error) {
	var state identity.RoleState
	err := s.pool.QueryRow(ctx, `
		SELECT guild_id,user_id,role_id,bot_added_role,manual_marked,last_known_present
		FROM identity_role_state WHERE guild_id=$1 AND user_id=$2 AND role_id=$3`, guildID, userID, roleID).
		Scan(&state.GuildID, &state.UserID, &state.RoleID, &state.BotAddedRole, &state.ManualMarked, &state.LastKnownPresent)
	if errors.Is(err, pgx.ErrNoRows) {
		state.GuildID, state.UserID, state.RoleID = guildID, userID, roleID
		return state, nil
	}
	return state, err
}

func (s *Store) ObserveRolePresence(ctx context.Context, guildID, userID, roleID string, present bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity_role_state(
			guild_id,user_id,role_id,bot_added_role,manual_marked,last_known_present,created_at,updated_at
		)
		SELECT $1,$2,$3,
			EXISTS (
				SELECT 1 FROM identity_role_grants
				WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND bot_added_role=true
			),
			$4 AND NOT EXISTS (
				SELECT 1 FROM identity_role_grants
				WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND bot_added_role=true
			),
			$4,now(),now()
		ON CONFLICT (guild_id,user_id,role_id) DO UPDATE SET
			manual_marked=CASE
				WHEN $4=true AND identity_role_state.bot_added_role=true AND identity_role_state.last_known_present=false THEN true
				WHEN $4=true AND identity_role_state.bot_added_role=false THEN true
				ELSE identity_role_state.manual_marked
			END,
			bot_added_role=CASE
				WHEN $4=true AND identity_role_state.bot_added_role=true AND identity_role_state.last_known_present=false THEN false
				ELSE identity_role_state.bot_added_role
			END,
			last_known_present=$4, updated_at=now()`, guildID, userID, roleID, present)
	return err
}

func (s *Store) MarkBotAdded(ctx context.Context, guildID, userID, roleID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity_role_state(guild_id,user_id,role_id,bot_added_role,manual_marked,last_known_present,created_at,updated_at)
		VALUES ($1,$2,$3,true,false,true,now(),now())
		ON CONFLICT (guild_id,user_id,role_id) DO UPDATE SET
			bot_added_role=true,manual_marked=false,last_known_present=true,updated_at=now()`, guildID, userID, roleID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants
		SET bot_added_role=true,updated_at=now()
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3 AND action=$4 AND matched=true`,
		guildID, userID, roleID, identity.ActionAddRole); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkBotRemoved(ctx context.Context, guildID, userID, roleID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_state
		SET bot_added_role=false,manual_marked=false,last_known_present=false,updated_at=now()
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3`, guildID, userID, roleID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants
		SET bot_added_role=false,updated_at=now()
		WHERE guild_id=$1 AND user_id=$2 AND role_id=$3`, guildID, userID, roleID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordAudit(ctx context.Context, audit identity.AuditIntent) error {
	return insertAudit(ctx, s.pool, audit)
}

func insertAudit(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, audit identity.AuditIntent) error {
	metadata, err := json.Marshal(audit.Metadata)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO identity_audit_events
			(event_type,dedupe_key,guild_id,actor_id,user_id,role_id,rule_id,source_type,action,result,error,metadata,created_at)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),$12,now())
		ON CONFLICT (dedupe_key) DO NOTHING`, audit.EventType, audit.DedupeKey, audit.GuildID, audit.ActorID,
		audit.UserID, audit.RoleID, audit.RuleID, audit.Source, audit.Action, audit.Result, audit.Error, metadata)
	return err
}

func (s *Store) EnsureGuild(ctx context.Context, guildID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO guild_configs(guild_id,created_at,updated_at) VALUES($1,now(),now()) ON CONFLICT(guild_id) DO NOTHING`, guildID)
	return err
}

func (s *Store) CreateVanityRule(ctx context.Context, rule identity.VanityRule) error {
	normalization, err := json.Marshal(rule.Normalization)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO vanity_rules(id,guild_id,name,word,source,comparison,role_id,action,enabled,priority,normalization,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),now())`, rule.ID, rule.GuildID, rule.Name, rule.Word,
		rule.Source, rule.Comparison, rule.RoleID, rule.Action, rule.Enabled, rule.Priority, normalization, rule.CreatedBy)
	return err
}

func (s *Store) CreateGuildTagRule(ctx context.Context, rule identity.GuildTagRule) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO guildtag_rules(id,guild_id,name,condition,value,role_id,action,enabled,priority,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),now())`, rule.ID, rule.GuildID, rule.Name, rule.Condition,
		rule.Value, rule.RoleID, rule.Action, rule.Enabled, rule.Priority, rule.CreatedBy)
	return err
}

func (s *Store) UpdateVanityRule(ctx context.Context, guildID, name string, word *string, source *identity.VanitySource, comparison *identity.Comparison, roleID *string, enabled *bool) error {
	return s.updateRuleAndInvalidate(ctx, identity.SourceVanity, guildID, name, func(tx pgx.Tx) (string, error) {
		var ruleID string
		err := tx.QueryRow(ctx, `
			UPDATE vanity_rules SET
				word=COALESCE($3,word), source=COALESCE($4,source), comparison=COALESCE($5,comparison), role_id=COALESCE($6,role_id),
				enabled=COALESCE($7,enabled), updated_at=now()
			WHERE guild_id=$1 AND name=$2 AND deleted_at IS NULL
			RETURNING id`, guildID, name, word, source, comparison, roleID, enabled).Scan(&ruleID)
		return ruleID, err
	})
}

func (s *Store) UpdateGuildTagRule(ctx context.Context, guildID, name string, condition *identity.GuildTagCondition, value *string, roleID *string, enabled *bool) error {
	return s.updateRuleAndInvalidate(ctx, identity.SourceGuildTag, guildID, name, func(tx pgx.Tx) (string, error) {
		var ruleID string
		err := tx.QueryRow(ctx, `
			UPDATE guildtag_rules SET condition=COALESCE($3,condition), value=COALESCE($4,value), role_id=COALESCE($5,role_id), enabled=COALESCE($6,enabled), updated_at=now()
			WHERE guild_id=$1 AND name=$2 AND deleted_at IS NULL
			RETURNING id`, guildID, name, condition, value, roleID, enabled).Scan(&ruleID)
		return ruleID, err
	})
}

func (s *Store) updateRuleAndInvalidate(ctx context.Context, source identity.Source, guildID, name string, update func(pgx.Tx) (string, error)) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ruleID, err := update(tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s rule %q not found", source, name)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants SET matched=false,last_evaluated_at=now(),updated_at=now()
		WHERE guild_id=$1 AND rule_id=$2 AND source_type=$3 AND matched=true`, guildID, ruleID, source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResetGuild(ctx context.Context, guildID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Rules are deleted by the guild_configs cascade, but grants intentionally
	// have no foreign key so audit/recovery history survives. Mark every live
	// grant inactive in the same transaction before removing configuration.
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants
		SET matched=false,last_evaluated_at=now(),updated_at=now()
		WHERE guild_id=$1 AND matched=true`, guildID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM guild_configs WHERE guild_id=$1`, guildID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteRuleByName(ctx context.Context, source identity.Source, guildID, name string) error {
	table := "vanity_rules"
	if source == identity.SourceGuildTag {
		table = "guildtag_rules"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ruleID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE %s SET enabled=false,deleted_at=now(),updated_at=now()
		WHERE guild_id=$1 AND name=$2 AND deleted_at IS NULL
		RETURNING id`, table), guildID, name).Scan(&ruleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s rule %q not found", source, name)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants SET matched=false,last_evaluated_at=now(),updated_at=now()
		WHERE guild_id=$1 AND rule_id=$2 AND source_type=$3 AND matched=true`, guildID, ruleID, source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteRule(ctx context.Context, source identity.Source, guildID, ruleID string) error {
	table := "vanity_rules"
	if source == identity.SourceGuildTag {
		table = "guildtag_rules"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET enabled=false,deleted_at=now(),updated_at=now() WHERE guild_id=$1 AND id=$2 AND deleted_at IS NULL", table), guildID, ruleID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("%s rule %q not found", source, ruleID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_role_grants SET matched=false,last_evaluated_at=now(),updated_at=now()
		WHERE guild_id=$1 AND rule_id=$2 AND source_type=$3 AND matched=true`, guildID, ruleID, source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type LogConfig struct {
	GuildID   string
	ChannelID string
	Events    map[string]bool
}

type LogEmbedBinding struct {
	GuildID      string
	EventKey     string
	TemplateID   string
	TemplateName string
	CreatedBy    string
}

type NotificationConfig struct {
	GuildID   string
	Source    identity.Source
	ChannelID string
	EmbedID   string
	EmbedName string
	Ping      string
}

func (s *Store) GetNotificationConfig(ctx context.Context, guildID string, source identity.Source) (NotificationConfig, error) {
	var config NotificationConfig
	err := s.pool.QueryRow(ctx, `
		SELECT n.guild_id,n.source_type,n.channel_id,COALESCE(n.embed_id,''),COALESCE(t.name,''),n.ping
		FROM notification_configs n
		LEFT JOIN embed_templates t ON t.id=n.embed_id AND t.enabled=true AND t.deleted_at IS NULL
		WHERE n.guild_id=$1 AND n.source_type=$2`, guildID, source).
		Scan(&config.GuildID, &config.Source, &config.ChannelID, &config.EmbedID, &config.EmbedName, &config.Ping)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationConfig{GuildID: guildID, Source: source, Ping: "user"}, nil
	}
	return config, err
}

func (s *Store) SetNotificationConfig(ctx context.Context, config NotificationConfig) error {
	var embedID any
	if config.EmbedID != "" {
		embedID = config.EmbedID
	}
	ping := config.Ping
	if ping == "" {
		ping = "user"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_configs(guild_id,source_type,channel_id,embed_id,ping,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,now(),now())
		ON CONFLICT(guild_id,source_type) DO UPDATE SET
			channel_id=EXCLUDED.channel_id,embed_id=EXCLUDED.embed_id,ping=EXCLUDED.ping,updated_at=now()`,
		config.GuildID, config.Source, config.ChannelID, embedID, ping)
	return err
}

func (s *Store) GetLogConfig(ctx context.Context, guildID string) (LogConfig, error) {
	var config LogConfig
	var events []byte
	err := s.pool.QueryRow(ctx, `SELECT guild_id,channel_id,events FROM log_configs WHERE guild_id=$1`, guildID).
		Scan(&config.GuildID, &config.ChannelID, &events)
	if errors.Is(err, pgx.ErrNoRows) {
		return LogConfig{GuildID: guildID, Events: map[string]bool{}}, nil
	}
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(events, &config.Events)
	return config, err
}

func (s *Store) SetLogConfig(ctx context.Context, config LogConfig) error {
	events, err := json.Marshal(config.Events)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO log_configs(guild_id,channel_id,events,created_at,updated_at) VALUES($1,$2,$3,now(),now())
		ON CONFLICT(guild_id) DO UPDATE SET channel_id=EXCLUDED.channel_id,events=EXCLUDED.events,updated_at=now()`,
		config.GuildID, config.ChannelID, events)
	return err
}

func (s *Store) GetLogEmbedBinding(ctx context.Context, guildID, eventKey string) (LogEmbedBinding, error) {
	var binding LogEmbedBinding
	err := s.pool.QueryRow(ctx, `
		SELECT b.guild_id,b.event_key,b.template_id,t.name,b.created_by
		FROM log_embed_bindings b
		JOIN embed_templates t ON t.id=b.template_id
		WHERE b.guild_id=$1 AND b.event_key=$2 AND t.enabled=true AND t.deleted_at IS NULL`, guildID, eventKey).
		Scan(&binding.GuildID, &binding.EventKey, &binding.TemplateID, &binding.TemplateName, &binding.CreatedBy)
	return binding, err
}

func (s *Store) ListLogEmbedBindings(ctx context.Context, guildID string) ([]LogEmbedBinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.guild_id,b.event_key,b.template_id,t.name,b.created_by
		FROM log_embed_bindings b
		JOIN embed_templates t ON t.id=b.template_id
		WHERE b.guild_id=$1 AND t.enabled=true AND t.deleted_at IS NULL
		ORDER BY b.event_key`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LogEmbedBinding, 0)
	for rows.Next() {
		var binding LogEmbedBinding
		if err := rows.Scan(&binding.GuildID, &binding.EventKey, &binding.TemplateID, &binding.TemplateName, &binding.CreatedBy); err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (s *Store) SetLogEmbedBinding(ctx context.Context, binding LogEmbedBinding) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO log_embed_bindings(guild_id,event_key,template_id,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,now(),now())
		ON CONFLICT(guild_id,event_key) DO UPDATE SET template_id=EXCLUDED.template_id,created_by=EXCLUDED.created_by,updated_at=now()`,
		binding.GuildID, binding.EventKey, binding.TemplateID, binding.CreatedBy)
	return err
}

func (s *Store) DeleteLogEmbedBinding(ctx context.Context, guildID, eventKey string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM log_embed_bindings WHERE guild_id=$1 AND event_key=$2`, guildID, eventKey)
	return err
}

type BotProfile struct {
	GuildID    string
	Nickname   *string
	AvatarRef  *string
	BannerRef  *string
	Bio        *string
	SyncStatus string
	LastError  string
	UpdatedAt  time.Time
}

func (s *Store) GetBotProfile(ctx context.Context, guildID string) (BotProfile, error) {
	var profile BotProfile
	err := s.pool.QueryRow(ctx, `SELECT guild_id,nickname,avatar_ref,banner_ref,bio,sync_status,last_error,updated_at FROM bot_profiles WHERE guild_id=$1`, guildID).
		Scan(&profile.GuildID, &profile.Nickname, &profile.AvatarRef, &profile.BannerRef, &profile.Bio, &profile.SyncStatus, &profile.LastError, &profile.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BotProfile{GuildID: guildID, SyncStatus: "not_configured"}, nil
	}
	return profile, err
}

func (s *Store) SaveBotProfile(ctx context.Context, profile BotProfile) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO bot_profiles(guild_id,nickname,avatar_ref,banner_ref,bio,sync_status,last_error,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,now(),now())
		ON CONFLICT(guild_id) DO UPDATE SET nickname=EXCLUDED.nickname,avatar_ref=EXCLUDED.avatar_ref,
		banner_ref=EXCLUDED.banner_ref,bio=EXCLUDED.bio,sync_status=EXCLUDED.sync_status,last_error=EXCLUDED.last_error,updated_at=now()`,
		profile.GuildID, profile.Nickname, profile.AvatarRef, profile.BannerRef, profile.Bio, profile.SyncStatus, profile.LastError)
	return err
}

type EmbedTemplate struct {
	ID        string
	GuildID   string
	Name      string
	Payload   []byte
	Enabled   bool
	CreatedBy string
}

func (s *Store) ListEmbedTemplates(ctx context.Context, guildID string) ([]EmbedTemplate, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,guild_id,name,payload,enabled,created_by FROM embed_templates WHERE guild_id=$1 AND deleted_at IS NULL ORDER BY name`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EmbedTemplate, 0)
	for rows.Next() {
		var item EmbedTemplate
		if err := rows.Scan(&item.ID, &item.GuildID, &item.Name, &item.Payload, &item.Enabled, &item.CreatedBy); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetEmbedTemplate(ctx context.Context, guildID, name string) (EmbedTemplate, error) {
	var item EmbedTemplate
	err := s.pool.QueryRow(ctx, `SELECT id,guild_id,name,payload,enabled,created_by FROM embed_templates WHERE guild_id=$1 AND name=$2 AND deleted_at IS NULL`, guildID, name).
		Scan(&item.ID, &item.GuildID, &item.Name, &item.Payload, &item.Enabled, &item.CreatedBy)
	return item, err
}

func (s *Store) GetEmbedTemplateByID(ctx context.Context, guildID, id string) (EmbedTemplate, error) {
	var item EmbedTemplate
	err := s.pool.QueryRow(ctx, `SELECT id,guild_id,name,payload,enabled,created_by FROM embed_templates WHERE guild_id=$1 AND id=$2 AND deleted_at IS NULL`, guildID, id).
		Scan(&item.ID, &item.GuildID, &item.Name, &item.Payload, &item.Enabled, &item.CreatedBy)
	return item, err
}

func (s *Store) SaveEmbedTemplate(ctx context.Context, item EmbedTemplate) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO embed_templates(id,guild_id,name,payload,enabled,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,now(),now())
		ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,payload=EXCLUDED.payload,enabled=EXCLUDED.enabled,updated_at=now()`,
		item.ID, item.GuildID, item.Name, item.Payload, item.Enabled, item.CreatedBy)
	return err
}

func (s *Store) DeleteEmbedTemplate(ctx context.Context, guildID, name string) error {
	_, err := s.pool.Exec(ctx, `UPDATE embed_templates SET enabled=false,deleted_at=now(),updated_at=now() WHERE guild_id=$1 AND name=$2 AND deleted_at IS NULL`, guildID, name)
	return err
}
