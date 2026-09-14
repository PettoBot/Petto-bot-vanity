CREATE TABLE IF NOT EXISTS log_embed_bindings (
    guild_id text NOT NULL REFERENCES guild_configs(guild_id) ON DELETE CASCADE,
    event_key text NOT NULL CHECK (event_key IN ('vanity_add','vanity_remove','tag_add','tag_remove','error')),
    template_id text NOT NULL REFERENCES embed_templates(id) ON DELETE CASCADE,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, event_key)
);
CREATE INDEX IF NOT EXISTS log_embed_bindings_template_idx ON log_embed_bindings(template_id);
