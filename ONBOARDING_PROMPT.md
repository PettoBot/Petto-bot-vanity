# Prompt de onboarding: Vanity Tag Bot

Quiero que implementes en esta carpeta un bot nuevo, independiente y de producción para Discord llamado provisionalmente **Vanity Tag Bot**.

Este bot no es una modificación de Petto. Petto solo sirve como referencia visual y de producto. No reutilices su base de datos, sus secretos, su cliente de Discord en ejecución ni módulos que creen acoplamiento entre ambos proyectos.

## 1. Objetivo principal

Construir un bot de Discord completo para administrar roles basados en:

1. Vanity: una palabra o condición presente en el perfil/nombre del usuario dentro del servidor.
2. Guild Tag/Server Tag: el usuario tiene activado el Server Tag de un servidor específico.

Debe permitir que múltiples reglas otorguen el mismo rol sin que una regla quite el rol mientras otra todavía lo justifica.

El bot debe ser real, funcional, idempotente, seguro, testeable y apto para desplegarse en Discloud junto con una base PostgreSQL privada en VLAN.

## 2. Restricciones de arquitectura

- Runtime: Go.
- Librería de Discord: `github.com/bwmarrin/discordgo`, salvo que una limitación comprobable exija una implementación directa con REST y Gateway.
- Base de datos: PostgreSQL independiente, exclusiva para este bot.
- Driver de PostgreSQL: `pgx/v5` o equivalente moderno.
- No usar Node.js para el bot.
- No usar otra base de datos compartida con Petto.
- No copiar secretos, `.env`, tokens, claves de Supabase ni credenciales del proyecto Petto.
- No crear una segunda base de datos lógica dentro de la base de Petto.
- Todas las consultas deben usar parámetros, contextos con timeout y transacciones cuando exista más de una mutación relacionada.
- Toda migración debe ser segura para repetirse.
- La configuración debe cargarse desde variables de entorno y validarse al inicio.

## 3. Interfaz de Discord

Usa comandos slash y componentes de Discord. No agregues un sistema público de comandos de prefix para esta primera versión.

### Comandos mínimos

```text
/cmds [comando]
/setup
/config view
/config reset
/set view
/set nickname
/set avatar
/set banner
/set bio
/set reset

/vanity add
/vanity edit
/vanity remove
/vanity list
/vanity test
/vanity sync
/vanity notify

/guildtag add
/guildtag edit
/guildtag remove
/guildtag list
/guildtag test
/guildtag sync
/guildtag notify

/identity status
/identity sync
/identity audit

/logs setup
/logs set
/logs view
/logs test

/embed create
/embed edit
/embed delete
/embed list
/embed preview
/embed send
/embed variables
```

Los nombres de subcomandos pueden ajustarse si Discord impone límites de opciones, pero la intención y los permisos deben mantenerse.

### `/cmds`

Implementa un panel de ayuda inspirado en Petto:

- menú por categorías;
- búsqueda por comando;
- detalle de uso, permisos, opciones y ejemplos;
- botones de navegación;
- timeout del collector/interacción;
- componentes V2 cuando la librería lo permita;
- emojis de Petto configurados mediante variables de entorno, con fallback Unicode;
- no mostrar comandos internos, diagnósticos privados ni datos de base de datos.

Categorías iniciales:

- Setup
- Identity
- Vanity
- Guild Tags
- Logs
- Embeds
- Utility

## 4. Modelo funcional de Vanity

Una regla Vanity debe incluir como mínimo:

- `id`;
- `guild_id`;
- `name`;
- `word` o expresión de comparación;
- fuente del perfil;
- modo de comparación;
- `role_id`;
- `enabled`;
- prioridad opcional;
- creador y timestamps.

### Fuentes soportadas inicialmente

- `custom_status`, el texto del estado personalizado de Discord que aparece en el perfil;
- `username`;
- `global_name`;
- `guild_nickname`;
- `display_name` resuelto con la misma prioridad que Discord usa para mostrarlo.

`custom_status` es una fuente explícita de Vanity. Requiere el intent privilegiado `GUILD_PRESENCES`, reacciona a `PRESENCE_UPDATE` y debe quitar el rol cuando el usuario cambia o elimina ese texto. No debe hacer polling ni usar la API de Server Tags.

### Comparaciones soportadas

- `equals`;
- `contains`;
- `starts_with`;
- `ends_with`;
- `regex` solo si se valida con límites de tiempo y longitud.

Normaliza case y espacios de forma configurable. No elimines Unicode arbitrariamente. La regla debe guardar la normalización aplicada para que el resultado sea reproducible.

### Acciones Vanity

- añadir rol;
- quitar rol cuando deje de cumplirse;
- registrar acción en el canal de logs;
- modo prueba sin mutar roles;
- evaluación de un miembro específico;
- sincronización manual del servidor.

## 5. Modelo funcional de Guild Tag

Discord expone en el objeto de usuario `primary_guild` con:

- `identity_guild_id`;
- `identity_enabled`;
- `tag`;
- `badge`.

La implementación debe usar esos campos y no asumir que cualquier string de nickname es un Guild Tag. Discord limita el texto del tag a cuatro caracteres.

### Condiciones Guild Tag

Implementa condiciones claras y tipadas:

- `is_guild_id`;
- `is_not_guild_id`;
- `identity_enabled`;
- `identity_disabled`;
- `tag_equals`;
- `tag_not_equals`.

Ejemplos conceptuales:

```text
/guildtag add condition:is_guild_id value:707307527846625280 action:add_role role:@rep
/guildtag add condition:is_not_guild_id value:707307527846625280 action:remove_role role:@rep
/guildtag add condition:tag_equals value:CINN action:add_role role:@cinnamochi
```

No uses `is_not_guild_id` para quitar un rol de forma ciega. Debe pasar por el motor de fuentes compartidas descrito en la sección 6.

## 6. Regla crítica de roles compartidos

Este requisito es obligatorio y debe tener pruebas automatizadas.

Un usuario puede cumplir simultáneamente:

- una regla Vanity que otorga `@rep`;
- una regla Guild Tag que también otorga `@rep`.

Si pierde Vanity pero conserva el Guild Tag, el bot debe conservar `@rep`.

Si pierde el Guild Tag pero conserva Vanity, el bot debe conservar `@rep`.

Solo si ya no existe ninguna fuente activa que justifique el rol puede evaluarse quitarlo.

### Diseño recomendado

Usa un ledger de grants por regla, no una sola columna por rol:

```text
identity_role_grants
- guild_id
- user_id
- role_id
- rule_id
- source_type: vanity | guildtag
- matched: boolean
- bot_added_role: boolean
- last_evaluated_at
- created_at
- updated_at
```

Reglas de seguridad:

1. Si una regla coincide y el usuario no tiene el rol, el bot intenta añadirlo y registra `bot_added_role=true` solo si la operación tuvo éxito.
2. Si una regla coincide y el usuario ya tenía el rol, no se debe asumir que el bot lo añadió. Registra la coincidencia, pero conserva `bot_added_role=false` si no existe prueba de propiedad.
3. Cuando una regla deja de coincidir, marca únicamente su grant como inactivo.
4. Antes de quitar un rol, consulta todos los grants activos de ese `guild_id + user_id + role_id`.
5. Si queda al menos un grant activo, no quites el rol.
6. Si no queda ninguno, quita el rol únicamente si el ledger demuestra que el bot lo añadió y no existe una marca de rol manual.
7. Si el rol fue asignado manualmente antes de que el bot lo gestionara, nunca lo borres automáticamente.
8. Todas las transiciones deben ser idempotentes y tolerar reintentos.
9. Usa una transacción para actualizar grants y registrar la intención. La mutación en Discord debe tener una estrategia de relectura/reconciliación por si la API falla entre pasos.
10. Si falta el rol, la regla o el usuario, registra un resultado recuperable, no una excepción no controlada.

### Resultado esperado

La decisión de rol debe parecerse a:

```text
active_sources(role) > 0  => role must exist
active_sources(role) == 0 and bot_owns_role(role) => remove role
active_sources(role) == 0 and not bot_owns_role(role) => leave role untouched
```

## 7. Eventos y sincronización

Implementa handlers para:

- `GUILD_CREATE`;
- `GUILD_MEMBER_ADD`;
- `GUILD_MEMBER_UPDATE`;
- `GUILD_MEMBER_REMOVE`;
- `USER_UPDATE` cuando Discord entregue cambios de `primary_guild`;
- interacción de botones y modales.

Importante: no hagas polling masivo de todos los miembros en cada reinicio. Los cambios de Server Tag no deben convertirse en una tormenta de requests.

### Estrategia

- Al entrar un miembro, evaluar reglas activas de ese servidor.
- Al cambiar nickname o datos de miembro, evaluar solo ese servidor y usuario.
- En `USER_UPDATE`, resolver solo las guilds en caché donde el usuario sea miembro y donde existan reglas relevantes.
- `/identity sync` permite sincronizar un usuario específico o una guild bajo permiso de administrador.
- `/vanity sync` y `/guildtag sync` deben mostrar progreso, límites y resultado.
- Un reconciliador periódico debe estar desactivado por defecto y tener concurrencia, delay y máximo de miembros configurables.
- Si no existe un evento confiable para un cambio concreto, documenta la limitación y ofrece sincronización manual, no polling agresivo.

## 8. Logs con el estilo de las imágenes

Crear un sistema de logs basado en embeds, inspirado en Petto y en estas formas:

### Vanity añadida

```text
Título: Vanity Action
Color: verde
Contenido: ✅ Added role @rep to @usuario
Campo: Word: cinnamochi
```

### Vanity eliminada

```text
Título: Vanity Action
Color: rojo
Contenido: ❌ Removed role @rep from @usuario
Campo: Word: cinnamochi
```

### Guild Tag añadida o eliminada

```text
Título: Server Tag Action
Color: verde o rojo
Contenido: ✅/❌ Added or removed role @rep from @usuario
Campo: Reason: Matched is_guild_id condition for value 707307527846625280
```

El log debe incluir, cuando exista:

- usuario con mención;
- ID de usuario en footer o campo secundario;
- rol con mención;
- regla y fuente;
- palabra/tag/condición;
- valor comparado;
- resultado;
- timestamp;
- error de Discord si la operación no pudo completarse.

No publiques tokens, URLs privadas de base de datos ni payloads que contengan secretos.

### Eventos de logs configurables

- `vanity_add`;
- `vanity_remove`;
- `tag_add`;
- `tag_remove`;
- `error`;
- `identity_role_add` (alias legacy);
- `identity_role_remove` (alias legacy);
- `identity_error` (alias legacy);

### Notificaciones de usuario separadas de los logs

Las notificaciones que recibe el miembro no deben configurarse dentro de `/logs`.
Implementa estos comandos con exactamente las opciones `channel`, `embed` y `ping`:

```text
/vanity notify channel:#vanity-notify embed:default ping:user
/guildtag notify channel:#tag-notify embed:default ping:user
```

- `channel` define dónde se envía el mensaje de usuario;
- `embed` acepta `default` o el nombre de un embed editable;
- `ping` acepta `user` o `none`;
- el mensaje se envía después de añadir correctamente el rol;
- `/vanity test`, `/guildtag test` y los dry-runs no deben enviar notificaciones;
- los embeds iniciales deben llamarse `vanity_notify` y `guildtag_notify`;
- `/logs` sólo debe enviar las tarjetas de auditoría `Vanity Action` y `Server Tag Action` mostradas arriba.

## 9. Sistema de embeds

Implementa un motor reusable de embeds como el de Petto, pero propio:

- plantillas por guild;
- `create`, `edit`, `delete`, `list`, `preview`, `send`;
- colores, título, descripción, thumbnail, image, footer, author y fields;
- variables seguras;
- validación de límites de Discord;
- modo Components V2 para paneles;
- vista previa antes de guardar o enviar;
- escape de menciones y control de `allowed_mentions`;
- fallback visual si una plantilla está incompleta.

Variables iniciales:

```text
{user}
{user.id}
{user.name}
{guild}
{guild.id}
{guild.icon}
{rule.name}
{rule.source}
{rule.value}
{role}
{vanity.rule}
{vanity.word}
{vanity.source}
{vanity.value}
{vanity.role}
{tag.rule}
{tag.condition}
{tag}
{tag.rule_value}
{tag.guild_id}
{tag.enabled}
{tag.badge}
{tag.role}
{identity.source}
{identity.value}
{timestamp}
```

## 10. Perfil personalizado del bot por servidor

Implementa una configuración `bot_profiles` por `guild_id` con:

- nickname del bot;
- avatar por servidor;
- banner por servidor;
- bio por servidor;
- URLs originales o referencia interna de assets;
- estado de sincronización;
- último error;
- timestamps.

### Comandos

El perfil se configura con el comando separado `/set`, no se debe mostrar como `/config profile` en la ayuda:

```text
/set view
/set nickname
/set avatar
/set banner
/set bio
/set reset
```

### Requisitos

- Validar MIME, tamaño, dimensiones y formato antes de convertir la imagen a data URI.
- Admitir PNG, JPEG, GIF si Discord lo permite para ese campo, y documentar las diferencias.
- No guardar imágenes grandes en PostgreSQL si pueden conservarse en almacenamiento privado; guardar solo una referencia segura.
- Aceptar tanto una URL HTTPS como un archivo adjunto de Discord para avatar y banner. No permitir URL y archivo al mismo tiempo.
- Si el usuario proporciona una URL, descargarla server-side con timeout, límite de bytes, validación de redirecciones y protección SSRF.
- Las subidas de Discord deben descargarse desde el CDN de Discord, validar MIME, tamaño y dimensiones, y convertirse a data URI antes de llamar a Discord.
- Nunca descargar `localhost`, IPs privadas, metadata endpoints ni dominios no permitidos sin una política explícita.
- Permitir borrar cada campo individualmente y volver al perfil global.
- Si actualizar avatar/banner/bio falla, no borrar la configuración anterior.
- Registrar cambios y errores en `bot_profile_update`.
- Evitar loops derivados de `GUILD_MEMBER_UPDATE` producido por el propio cambio del perfil del bot.
- Aplicar cambios con una cola por guild para que dos paneles no pisen la misma actualización.

La API oficial actual documenta `Modify Current Member` con `nick`, `avatar`, `banner` y `bio`. Implementa el endpoint mediante la librería o REST directo si la versión de `discordgo` no expone todos los campos.

## 11. Setup visual

`/setup` debe abrir un flujo visual privado para administradores:

- canal privado opcional `identity-setup` o respuesta ephemeral;
- configuración de canal de logs;
- creación de reglas Vanity;
- creación de reglas Guild Tag;
- selección de roles;
- perfil personalizado del bot;
- vista previa de embeds;
- botón de guardar;
- botón de cancelar;
- confirmación final con resumen.

El bot no debe enviar el token, secretos internos ni instrucciones de desarrollador en onboarding público o DMs normales.

## 12. Base de datos PostgreSQL

Crear migraciones idempotentes para, como mínimo:

```text
guild_configs
vanity_rules
guildtag_rules
identity_role_grants
identity_role_state
bot_profiles
embed_templates
log_configs
identity_audit_events
sync_jobs
```

Restricciones:

- `guild_id` siempre forma parte de las claves y consultas.
- Unique constraints para impedir reglas duplicadas por guild.
- Foreign keys cuando no impidan recuperar una configuración después de borrar una entidad de Discord.
- Índices por `(guild_id, enabled)`, `(guild_id, role_id)`, `(guild_id, user_id)`.
- JSONB solo para metadata flexible, no para la relación principal de grants.
- `created_at` y `updated_at` con timezone.
- Soft delete para reglas si hace falta auditoría.

## 13. Seguridad y despliegue privado

El bot correrá en Discloud y PostgreSQL debe vivir en la base privada de Discloud/VLAN.

- No expongas PostgreSQL a Internet pública.
- Permite conexiones únicamente desde el servicio del bot o la VLAN.
- Usa TLS si está disponible.
- Usa un usuario de base con permisos mínimos.
- Separa credenciales de desarrollo y producción.
- No escribas `DATABASE_URL` ni `DISCORD_TOKEN` en logs.
- Define health checks para Discord Gateway, PostgreSQL y migraciones.
- Cierra correctamente conexiones y señales de apagado.
- Añade límites de concurrencia para Discord REST.
- Respeta rate limits de Discord; nunca uses loops sin espera.
- Implementa retry con backoff solo para errores transitorios.
- Usa una cola por guild para mutaciones de roles y perfil.

## 14. Estructura sugerida

```text
cmd/bot/main.go
internal/config
internal/database
internal/discord
internal/features/identity
internal/features/logs
internal/features/embeds
internal/features/profile
internal/http
migrations
docs
```

Separar:

- registro de comandos;
- router de interacciones;
- cliente Discord;
- repositorios SQL;
- evaluadores de condiciones;
- role reconciler;
- renderizador de embeds;
- logger;
- perfil del bot;
- health checks.

## 15. Pruebas obligatorias

Antes de considerar terminado:

### Unitarias

- `contains`, `equals`, `starts_with`, `ends_with`;
- Guild Tag enabled/disabled;
- `is_guild_id` y `is_not_guild_id`;
- normalización Unicode y case;
- agregación de múltiples fuentes para un mismo rol;
- rol se conserva si queda otra fuente activa;
- rol se elimina solo si el bot es dueño y no quedan fuentes;
- rol manual nunca se elimina;
- reintento idempotente;
- no duplicar logs para el mismo resultado.

### Integración

- migraciones desde una base vacía;
- migraciones repetidas;
- reglas duplicadas;
- fallo de Discord durante add/remove role;
- fallo después de guardar grant y antes de mutar Discord;
- recuperación de configuración;
- actualización de avatar/banner/bio;
- URLs inválidas y SSRF;
- payloads de embeds al límite de Discord.

### Smoke test

Debe poder iniciar sin secretos impresos, conectarse a PostgreSQL, registrar comandos, responder `/cmds`, abrir `/setup` y evaluar una regla en modo dry-run.

## 16. Entregables del primer ciclo

1. Scaffolding Go compilable.
2. Configuración y `.env.example`.
3. Migraciones PostgreSQL.
4. Registro de comandos slash.
5. `/cmds` visual.
6. `/setup` visual.
7. Motor Vanity.
8. Motor Guild Tag.
9. Ledger de roles compartidos.
10. Logs con embeds.
11. Motor de plantillas de embeds.
12. Perfil por servidor con avatar, banner, bio y nickname.
13. Pruebas unitarias e integración.
14. Health check y apagado seguro.
15. Documentación de despliegue en Discloud y conexión por VLAN.

## 17. Criterios de aceptación

El trabajo solo está terminado cuando:

- el bot compila con `go test ./...`;
- las migraciones son repetibles;
- todos los comandos públicos son slash;
- `/cmds` no muestra comandos internos;
- Vanity y Guild Tag pueden compartir el mismo rol sin removerlo incorrectamente;
- los logs se parecen al estilo de las imágenes de referencia;
- el sistema funciona sin Petto ni su base de datos;
- el perfil por servidor puede cambiar nickname, avatar, banner y bio con `/set`, usando URL o upload para imágenes, sin romper el fallback global;
- no hay secretos en el repositorio;
- el despliegue usa PostgreSQL privado por VLAN;
- los límites y rate limits de Discord se respetan;
- las limitaciones de eventos de Server Tag están documentadas y existe sincronización manual.

## 18. Referencias oficiales que deben verificarse durante la implementación

- User Object y `primary_guild`: `https://docs.discord.com/developers/resources/user`
- Guild Member y Modify Current Member: `https://docs.discord.com/developers/resources/guild`
- Server Tags: `https://support.discord.com/hc/en-us/articles/31444248479639-Server-Tags`
- Gateway events: `https://docs.discord.com/developers/events/gateway-events`
- Discord API rate limits: `https://docs.discord.com/developers/topics/rate-limits`

No inventes campos ni asumas que todos los cambios de Server Tag generan un evento. Comprueba la versión real de las dependencias y crea una prueba que confirme cómo llegan `primary_guild` y los eventos en producción de prueba.
