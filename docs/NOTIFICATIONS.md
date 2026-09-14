# Notificaciones de Vanity y Guild Tag

Las notificaciones para usuarios están separadas de los logs de auditoría.

## Configuración

Usa el comando de la fuente que quieres configurar:

```text
/vanity notify channel:#vanity-notify embed:default ping:user
/guildtag notify channel:#tag-notify embed:default ping:user
```

Opciones:

- `channel`: canal donde se enviará el mensaje al usuario.
- `embed`: `default` o el nombre de un embed creado con `/embed create`.
- `ping`: `user` para mencionar al miembro o `none` para no notificarlo.

Si ejecutas `/vanity notify` o `/guildtag notify` sin opciones, el bot muestra la configuración actual.

Los embeds predeterminados se llaman `vanity_notify` y `guildtag_notify`. Se crean automáticamente al configurar cada fuente y se pueden editar con:

```text
/embed edit name:vanity_notify
/embed edit name:guildtag_notify
```

La vista previa se puede comprobar con `/embed preview name:vanity_notify` o `/embed preview name:guildtag_notify`.

## Cuándo se envía

El mensaje se envía después de que el bot añade correctamente el rol correspondiente. No se envía durante `/vanity test` ni `/guildtag test`, porque esos comandos son dry-run.

Si Vanity y Guild Tag comparten un rol, el ledger de roles sigue evitando quitarlo mientras la otra fuente todavía lo necesita.

## Logs de auditoría

Los logs no configuran notificaciones de usuario. `/logs` sólo configura el canal de auditoría para tarjetas de acción:

```text
/logs setup
/logs view
/logs test event:vanity_add
/logs test event:tag_add
```

Estas tarjetas muestran `Vanity Action` o `Server Tag Action`, el rol añadido o removido, el usuario y la razón de la coincidencia.

## Variables principales

```text
{user} {user.mention} {user.id}
{role} {role.id}
{rule.name} {rule.source} {rule.value} {rule.condition} {rule.reason}
{vanity.rule} {vanity.word} {vanity.source} {vanity.value}
{tag.rule} {tag.condition} {tag} {tag.rule_value}
{action} {action.text} {result} {timestamp} {newline}
```

Si un embed personalizado no puede renderizarse, el bot usa un mensaje seguro de respaldo y no interrumpe la sincronización de roles.
