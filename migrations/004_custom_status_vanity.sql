ALTER TABLE vanity_rules DROP CONSTRAINT IF EXISTS vanity_rules_source_check;

ALTER TABLE vanity_rules
    ADD CONSTRAINT vanity_rules_source_check
    CHECK (source IN ('username','global_name','guild_nickname','display_name','custom_status'));
