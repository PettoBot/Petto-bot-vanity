CREATE TABLE IF NOT EXISTS guild_configs (
    guild_id text PRIMARY KEY,
    setup_channel_id text,
    setup_completed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS vanity_rules (
    id text PRIMARY KEY,
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    name text NOT NULL,
    word text NOT NULL,
    source text NOT NULL CHECK (source IN ('username','global_name','guild_nickname','display_name')),
    comparison text NOT NULL CHECK (comparison IN ('equals','contains','starts_with','ends_with','regex')),
    role_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('add_role','remove_role')),
    enabled boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 0,
    normalization jsonb NOT NULL DEFAULT '{"case_fold":true,"trim_space":true,"collapse_space":true}'::jsonb,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS vanity_rules_guild_name_unique
    ON vanity_rules(guild_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS vanity_rules_guild_enabled_idx ON vanity_rules(guild_id, enabled);
CREATE INDEX IF NOT EXISTS vanity_rules_guild_role_idx ON vanity_rules(guild_id, role_id);

CREATE TABLE IF NOT EXISTS guildtag_rules (
    id text PRIMARY KEY,
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    name text NOT NULL,
    condition text NOT NULL CHECK (condition IN ('is_guild_id','is_not_guild_id','identity_enabled','identity_disabled','tag_equals','tag_not_equals')),
    value text NOT NULL DEFAULT '',
    role_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('add_role','remove_role')),
    enabled boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 0,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS guildtag_rules_guild_name_unique
    ON guildtag_rules(guild_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS guildtag_rules_guild_enabled_idx ON guildtag_rules(guild_id, enabled);
CREATE INDEX IF NOT EXISTS guildtag_rules_guild_role_idx ON guildtag_rules(guild_id, role_id);

CREATE TABLE IF NOT EXISTS identity_role_grants (
    guild_id text NOT NULL,
    user_id text NOT NULL,
    role_id text NOT NULL,
    rule_id text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('vanity','guildtag')),
    action text NOT NULL CHECK (action IN ('add_role','remove_role')),
    matched boolean NOT NULL DEFAULT false,
    bot_added_role boolean NOT NULL DEFAULT false,
    last_evaluated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, user_id, role_id, rule_id)
);
CREATE INDEX IF NOT EXISTS identity_role_grants_guild_user_idx ON identity_role_grants(guild_id, user_id);
CREATE INDEX IF NOT EXISTS identity_role_grants_guild_role_idx ON identity_role_grants(guild_id, role_id);

CREATE TABLE IF NOT EXISTS identity_role_state (
    guild_id text NOT NULL,
    user_id text NOT NULL,
    role_id text NOT NULL,
    bot_added_role boolean NOT NULL DEFAULT false,
    manual_marked boolean NOT NULL DEFAULT false,
    last_known_present boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, user_id, role_id)
);
CREATE INDEX IF NOT EXISTS identity_role_state_guild_user_idx ON identity_role_state(guild_id, user_id);

CREATE TABLE IF NOT EXISTS bot_profiles (
    guild_id text PRIMARY KEY REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    nickname text,
    avatar_ref text,
    banner_ref text,
    bio text,
    sync_status text NOT NULL DEFAULT 'not_configured',
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS embed_templates (
    id text PRIMARY KEY,
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    name text NOT NULL,
    payload jsonb NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS embed_templates_guild_name_unique ON embed_templates(guild_id,name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS log_configs (
    guild_id text PRIMARY KEY REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    channel_id text NOT NULL,
    events jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity_audit_events (
    id bigserial PRIMARY KEY,
    event_type text NOT NULL,
    dedupe_key text NOT NULL,
    guild_id text,
    actor_id text,
    user_id text,
    role_id text,
    rule_id text,
    source_type text,
    action text,
    result text,
    error text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS identity_audit_events_dedupe_unique ON identity_audit_events(dedupe_key);
CREATE INDEX IF NOT EXISTS identity_audit_events_guild_created_idx ON identity_audit_events(guild_id,created_at DESC);

CREATE TABLE IF NOT EXISTS sync_jobs (
    id text PRIMARY KEY,
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    user_id text,
    source text NOT NULL,
    status text NOT NULL,
    processed integer NOT NULL DEFAULT 0,
    total integer,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sync_jobs_guild_status_idx ON sync_jobs(guild_id,status);
