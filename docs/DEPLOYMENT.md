# Private deployment

Vanity Tag Bot is a separate service. Use a separate Discord application/token and a separate PostgreSQL role/database. Do not copy a Petto `.env`, token, Supabase key, pool, or database URL.

## PostgreSQL over the private VLAN

Create the database inside Discloud's private network/VLAN and expose it only to the bot service. The database user should have only the privileges needed by this application's migrations and runtime. Prefer TLS (`sslmode=require`) and use a different password for development and production.

Set the production environment from the platform's secret store:

```text
DISCORD_TOKEN=<Vanity Tag Bot token>
DISCORD_APPLICATION_ID=<Vanity Tag Bot application id>
DATABASE_URL=postgresql://identity_bot:<private-password>@<private-host>:5432/vanity_tag_bot?sslmode=require
MIGRATIONS_DIR=migrations
HTTP_ADDR=:8080
SHARD_COUNT=1
SHARD_ID=0
PRESENCE_TEXT=Vanity & Guild Tags
```

Keep `ASSET_ALLOWED_HOSTS` explicit for external URLs used by `/set avatar` or `/set banner`. Discord CDN uploads are accepted automatically; an empty allowlist still rejects other remote profile assets. Never put credentials in `README.md`, migrations, logs, or onboarding responses.

## Startup and health

The process connects and pings PostgreSQL, applies migrations in sorted order, opens the Discord Gateway with `GUILDS`, `GUILD_MEMBERS`, and `GUILD_PRESENCES`, registers slash commands, then serves:

- `/healthz`: PostgreSQL connectivity;
- `/readyz`: PostgreSQL plus Discord `DataReady`.

Use the platform's HTTP health check against `/readyz`. A SIGTERM closes the health server, Gateway, and database pool with a bounded timeout.

Commands are registered globally so the public bot exposes the same slash-command catalog in every guild where it is installed. Global propagation is not immediate after a deployment. `SHARD_COUNT=1` and `SHARD_ID=0` run one shard. If the bot needs more shards, deploy one process per shard using the same `SHARD_COUNT` and distinct zero-based `SHARD_ID` values.

## Discord permissions and intents

Invite the new application with the permissions required by its configured roles and log channel, including Manage Roles and the ability to manage the bot's own nickname. Enable the Server Members and Presence intents in the Discord Developer Portal. Presence is required for `source:custom_status` rules to receive the user's Custom Status text when it changes. The bot does not use prefix commands.

The bot identifies its Gateway session as Android so Discord can render the mobile phone badge while its status is `online`. This is a client-identification compatibility workaround, not a field in the Update Presence payload.

Role changes are serialized per guild and `discordgo`'s REST limiter/retries are enabled. The engine writes a grant intent before the Discord mutation and reconciles the next evaluation if Discord fails after the database transaction.

## Server Tag limitation

Discord's `primary_guild` object contains `identity_guild_id`, nullable `identity_enabled`, `tag`, and `badge`; `tag` is limited to four characters. The current `discordgo` typed User model does not include this newer object, so raw `USER_UPDATE` decoding is used. Discord does not guarantee that every Server Tag change arrives as a guild-member event. The bot therefore avoids restart-time member polling, processes available events, and exposes `/identity sync`, `/vanity sync`, and `/guildtag sync` as bounded manual recovery commands. Periodic reconciliation stays off unless the environment explicitly enables it.

## Safe rollout

1. Fill in a new `.env` from `.env.example` with the new application's credentials and private database URL.
2. Run `go test ./...` and `go vet ./...` in the build step.
3. Start one instance and verify `/readyz` before inviting it to production guilds.
4. Run `/setup`, configure `/logs setup` for action audit cards, then configure `/vanity notify` and `/guildtag notify` for user notifications. Test the audit channel with `/logs test event:vanity_add` and `/logs test event:tag_add`.
5. Edit `guildtag_notify` or `vanity_notify` with `/embed edit`, then verify a real transition with `/vanity sync` or `/guildtag sync`.
6. Enable periodic reconciliation only after observing Gateway events and rate-limit behavior in a test guild.

## Official references

- [User Object and `primary_guild`](https://docs.discord.com/developers/resources/user)
- [Guild Member and Modify Current Member](https://docs.discord.com/developers/resources/guild)
- [Gateway Events](https://docs.discord.com/developers/events/gateway-events)
- [Discord API rate limits](https://docs.discord.com/developers/topics/rate-limits)
- [Server Tags support article](https://support.discord.com/hc/en-us/articles/31444248479639-Server-Tags)
