# Vanity Tag Bot

Bot independiente de Discord para gestionar reglas de Vanity y Guild Tags por servidor.

Este proyecto no comparte código de ejecución, base de datos ni secretos con Petto. La referencia visual se limita a embeds oscuros, emojis configurables, tarjetas de acción, componentes y navegación de ayuda.

## Incluido

- Go como runtime, `discordgo` para Gateway/REST y `pgx/v5` para PostgreSQL.
- Sólo comandos slash y componentes de Discord.
- Migración PostgreSQL idempotente y repositorios con consultas parametrizadas y contextos.
- Reglas Vanity y Guild Tag con evaluación dry-run, sincronización manual y eventos de miembro y presencia.
- Ledger de grants por regla: Vanity y Guild Tag pueden compartir un rol sin removerlo incorrectamente.
- Logs en embeds, auditoría deduplicada y plantillas de embeds con variables seguras.
- Perfil por servidor del bot mediante `/set`: nickname, avatar, banner y bio, con URL o upload de Discord, validación de activos y fallback global.
- Health checks `/healthz` y `/readyz`, cierre ordenado y reconciliación periódica desactivada por defecto.

## Desarrollo

```text
go test ./...
go vet ./...
go run .
```

El proceso exige `DISCORD_TOKEN`, `DISCORD_APPLICATION_ID` y una `DATABASE_URL` PostgreSQL exclusiva para este bot. Nunca apuntes a la base de Petto ni imprimas credenciales.

Los slash commands se registran siempre como comandos globales para que estén disponibles en todos los servidores donde esté instalada la aplicación. Discord puede tardar en propagar los comandos globales después del reinicio. `PRESENCE_TEXT` configura el estado personalizado del bot. `SHARD_COUNT=1` y `SHARD_ID=0` son correctos para una instancia; al escalar, ejecuta una instancia por shard con el mismo `SHARD_COUNT` y un `SHARD_ID` distinto.

## Despliegue

Consulta [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) para PostgreSQL privado por VLAN, permisos, intents, health checks y límites de Server Tag. `.env.example` contiene placeholders; los secretos reales sólo deben existir en el entorno de despliegue.

## Decisiones

Consulta [docs/DECISIONS.md](docs/DECISIONS.md) para la propiedad de roles compartidos, compatibilidad con `primary_guild`, límites de sincronización y seguridad de activos.

## Uso rápido en Discord

```text
/vanity add name:rep word:cinnamochi source:custom_status comparison:contains role:@rep action:add_role
/guildtag add name:partner condition:is_guild_id value:707307527846625280 role:@partner action:add_role
/vanity notify channel:#vanity-notify embed:default ping:user
/guildtag notify channel:#tag-notify embed:default ping:user
/logs setup
/logs test event:tag_add
```

Las notificaciones de usuario se configuran con `/vanity notify` y `/guildtag notify`. `/logs` queda reservado para tarjetas de auditoría de roles añadidos, removidos, razones y errores. Los embeds `vanity_notify` y `guildtag_notify` se pueden editar con `/embed edit` y probar con `/embed preview`.

En el editor, escribe por ejemplo `{tag}`, `{tag.guild_id}`, `{tag.enabled}`, `{vanity.word}` o `{vanity.value}`. Para una regla `source:custom_status`, `{vanity.value}` es el texto del estado personalizado que Discord muestra en el perfil del usuario. `/embed preview` y `/embed send` resuelven esas variables usando el miembro actual y sus reglas activas. Si no coincide ninguna regla, la variable queda vacía.

La presencia del bot se envía como `online` con `PRESENCE_TEXT`. La sesión Gateway se identifica como Android para que Discord muestre el indicador de móvil.
