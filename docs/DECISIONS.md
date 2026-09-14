# Decisions

## Commands

The public interface is slash-first:

- `/cmds`
- `/setup`
- `/vanity`
- `/guildtag`
- `/identity`
- `/logs`
- `/embed`
- `/config`
- `/set` for the bot's per-server nickname, avatar, banner, and bio

No public prefix parser is required. Internal jobs and buttons use private custom IDs.

## Identity sources

Vanity rules support `custom_status` for the user's Discord Custom Status text, plus the compatible profile sources `username`, `global_name`, `guild_nickname`, and the resolved display name. Custom Status evaluation requires the privileged `GUILD_PRESENCES` intent and reacts to `PRESENCE_UPDATE`; it does not poll Discord or consume the Server Tag API quota.

Guild Tag rules use the user's `primary_guild` data: `identity_guild_id`, nullable `identity_enabled`, `tag`, and `badge`. The implementation never treats arbitrary nickname text as a Server Tag. If Discord omits `primary_guild`, the source is unknown rather than an empty identity. Negative conditions (`is_not_guild_id`, `tag_not_equals`, and `identity_disabled`) require a real comparable value and do not match missing data.

## Shared role ownership

The role engine stores grants per rule, role, source, and action. Editing, disabling, or deleting a rule invalidates grants that no longer match that exact scope, including legacy grants left behind by an edited rule ID. A matching `remove_role` has priority over matching add grants for the same role. Physical removal is still allowed only when the ownership ledger says the bot added the role and it has not been manually re-added, so administrator/member roles are preserved. Reconciliation also revisits bot-managed roles even when the original rule no longer exists.

## Discord API compatibility

`discordgo v0.29.0` remains the Gateway/REST dependency. That release does not expose `primary_guild` in its typed `User` model, so the bot consumes the raw `USER_UPDATE` event and decodes the documented fields without replacing the Discord client. The current-member profile endpoint is sent through `Session.Request` because the dependency does not expose all of `nick`, `avatar`, `banner`, and `bio` in one typed helper.

The bot sends an explicit `status: online` presence with its custom status and identifies the Gateway session as Android so Discord renders the mobile indicator. The indicator is not a field in the Update Presence payload.

## Synchronization limits

`GUILD_MEMBER_ADD` and `GUILD_MEMBER_UPDATE` evaluate one member. `USER_UPDATE` evaluates only cached memberships in guilds the session knows about. Manual and periodic synchronization fetch guild members in Discord pages of at most 1,000, with a hard safety cap of 10,000 members per guild plus the configured maximum, per-page timeouts, serialized guild work, bounded worker concurrency, and Discord REST rate-limit retries. `/identity sync`, `/vanity sync`, and `/guildtag sync` remain the explicit recovery path for Server Tag changes that do not arrive as a reliable member event.

## Profile assets

Avatar and banner URLs must be HTTPS and match `ASSET_ALLOWED_HOSTS`; Discord CDN/media hosts used by attachment options are a built-in trusted allowlist. DNS resolution rejects local/private/metadata targets, redirects are revalidated, and the dialer refuses private resolved addresses. Downloads are bounded and the actual bytes—not the HTTP `Content-Type` header—determine whether the payload is PNG, JPEG, or GIF and which MIME is used in the Discord data URI. Unsupported bytes are rejected. On a failed Discord update the prior database references remain intact while only sync status/error is updated.

## Storage boundary

The PostgreSQL schema is created by this repository's migrations and has no connection to Petto. Rules use soft delete where auditability matters; grants deliberately do not have Discord-entity foreign keys so an entity deletion cannot destroy recovery history.
