CREATE TABLE IF NOT EXISTS notification_configs (
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    source_type text NOT NULL CHECK (source_type IN ('vanity','guildtag')),
    channel_id text NOT NULL,
    embed_id text REFERENCES embed_templates(id) ON DELETE SET NULL,
    ping text NOT NULL DEFAULT 'user' CHECK (ping IN ('user','none')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, source_type)
);

CREATE INDEX IF NOT EXISTS notification_configs_embed_idx ON notification_configs(embed_id);
