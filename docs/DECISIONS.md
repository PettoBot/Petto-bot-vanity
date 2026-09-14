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

Guild Tag rules use the user's `primary_guild` data: `identity_guild_id`, nullable `identity_enabled`, `tag`, and `badge`. The implementation never treats arbitrary nickname text as a Server Tag.

## Shared role ownership

The role engine stores grants per rule, not only per role. A role is removable only when every active add grant for that role is gone and the bot previously recorded that it added the role. A role that was already present remains untouched. This prevents a Vanity rule from removing a role that is still justified by a Guild Tag rule.

## Discord API compatibility

`discordgo v0.29.0` remains the Gateway/REST dependency. That release does not expose `primary_guild` in its typed `User` model, so the bot consumes the raw `USER_UPDATE` event and decodes the documented fields without replacing the Discord client. The current-member profile endpoint is sent through `Session.Request` because the dependency does not expose all of `nick`, `avatar`, `banner`, and `bio` in one typed helper.

The bot sends an explicit `status: online` presence with its custom status and identifies the Gateway session as Android so Discord renders the mobile indicator. The indicator is not a field in the Update Presence payload.

## Synchronization limits

`GUILD_MEMBER_ADD` and `GUILD_MEMBER_UPDATE` evaluate one member. `USER_UPDATE` evaluates only cached memberships in guilds the session knows about. A periodic reconciler is disabled by default and, when enabled, remains bounded by interval, concurrency, and maximum members. `/identity sync`, `/vanity sync`, and `/guildtag sync` are the explicit recovery path for Server Tag changes that do not arrive as a reliable member event.

## Profile assets

Avatar and banner URLs must be HTTPS and match `ASSET_ALLOWED_HOSTS`, except for Discord CDN attachment URLs from `/set avatar` and `/set banner`. DNS resolution rejects local/private/metadata targets, downloads are bounded, and MIME/dimensions are decoded before a data URI is sent to Discord. On a failed Discord update the prior database references remain intact while only sync status/error is updated.

## Storage boundary

The PostgreSQL schema is created by this repository's migrations and has no connection to Petto. Rules use soft delete where auditability matters; grants deliberately do not have Discord-entity foreign keys so an entity deletion cannot destroy recovery history.
