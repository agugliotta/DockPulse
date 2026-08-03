# Uso, operación y troubleshooting

Esta guía describe el comportamiento operativo del MVP. Las reglas críticas se aplican en el control plane y vuelven a validarse en el agente inmediatamente antes de tocar Docker.

## Rutina recomendada

1. Revisar **Agents**: no ejecutar cambios en agentes `degraded` u `offline`.
2. Ejecutar **Refresh inventory** en el agente objetivo.
3. Filtrar workloads con update y abrir el detalle.
4. Revisar **Update readiness**: el preflight muestra agent, scope, políticas, contrato y digest.
5. Ejecutar dry-run y revisar todos los pasos y warnings. Dry-run es una inspección; no cambia Docker.
6. Confirmar **Update now** solo después de revisar el dry-run para el mismo scope.
7. Para una primera intervención, actualizar un servicio no crítico antes que un stack completo.
8. Seguir el job hasta estado terminal y revisar logs del servicio después de DockPulse.

DockPulse confirma que el contenedor quedó `running`; no confirma que la aplicación responda correctamente. La validación funcional posterior sigue siendo responsabilidad del operador.

## Significado de estados

### Agentes

| Estado     | Condición                                   | Acción sugerida                       |
| ---------- | ------------------------------------------- | ------------------------------------- |
| `healthy`  | heartbeat recibido hace 45 segundos o menos | Operación normal                      |
| `degraded` | heartbeat entre 45 y 90 segundos            | Revisar red/carga antes de actualizar |
| `offline`  | sin heartbeat por más de 90 segundos        | No se despachan updates               |

La freshness usa el reloj del control plane, no el timestamp informado por el agente.

### Workloads

| Indicador          | Significado                                                          |
| ------------------ | -------------------------------------------------------------------- |
| `update available` | digest local y remoto conocidos, y diferentes                        |
| `ignored`          | excluido operativamente; no se actualiza                             |
| `protected`        | bloqueo explícito para evitar mutaciones accidentales                |
| `sensitive`        | base de datos, cache o label explícita; requiere opt-in adicional    |
| `manageable=false` | el agente no puede reconstruir/administrar el objetivo con seguridad |

`ignored` y `protected` bloquean updates; se mantienen separados para expresar intención operativa. Un error de registry puede aparecer en `safety_reason` sin volver no administrable al contenedor: significa que la evaluación de update quedó incompleta.

## Preflight y dry-run

La vista de detalle de un workload ejecuta un preflight para el scope seleccionado (`container`, `service` o `stack`). El preflight no toca Docker; resume las condiciones que pueden permitir, advertir o bloquear un update:

- conectividad y modo del Agent;
- scope válido para Docker run o Compose;
- contrato administrable y directorio Compose disponible;
- flags `ignored` y `protected`;
- política de workloads sensibles;
- comparación de digest local/remoto.

`Can dry-run` y `Can update` se calculan por separado. Un Agent en read-only puede permitir dry-run pero bloquear update; un workload no administrable bloquea ambos. La UI exige un dry-run exitoso para el mismo scope antes de habilitar **Update now**.

Dry-run crea una inspección auditable en Activity. Si aparece como `Inspection`, significa que DockPulse validó y guardó el plan, no que haya recreado contenedores.

## Detección de imágenes

El agente consulta `Docker-Content-Digest` del manifest asociado a la referencia actual. El método funciona con Docker Hub y registries v2 anónimos.

- Un tag mutable —incluido `latest`— no se considera seguro por sí mismo.
- `update_available=true` exige digest local y remoto conocidos y distintos.
- Sin `RepoDigest` local, sin header remoto o con error de registry, DockPulse no afirma que exista un update.
- Registries privados con autenticación no forman parte del MVP.
- Un cambio de digest puede incluir una reconstrucción del mismo tag; revisar release notes de la imagen fuera de DockPulse.

## Versiones y self-update

La vista **System** muestra:

- versión actual del control plane, tomada del binario en ejecución;
- versión actual de cada Agent, reportada por heartbeat;
- target configurado en `DOCKPULSE_UPDATE_VERSION`.

Si `DOCKPULSE_VERSION=latest`, Docker resuelve el digest exacto durante `docker compose pull`; DockPulse muestra el tag objetivo, no predice el digest antes del pull.

El self-update de Agents es opt-in. Requiere:

- `DOCKPULSE_READ_ONLY=false`;
- `DOCKPULSE_SELF_UPDATE_ENABLED=true`;
- `DOCKPULSE_SELF_UPDATE_DIR`, `DOCKPULSE_SELF_UPDATE_PROJECT` y `DOCKPULSE_SELF_UPDATE_SERVICE` apuntando al Compose que ejecuta ese Agent.

Desde **System**, el botón **Self-update** crea un job auditable y el Agent ejecuta:

```text
docker compose --project-directory <dir> -p <project> pull <service>
docker compose --project-directory <dir> -p <project> up -d <service>
```

Para actualizar el Server con el mismo flujo, debe existir un Agent local en el LXC del Server configurado contra el servicio `dockpulse-control`. El Server por sí mismo no toca Docker.

## Políticas y labels

### Docker run

Opt-in básico:

```bash
--label io.dockpulse.manage=true
```

Para un workload sensible:

```bash
--label io.dockpulse.manage=true \
--label io.dockpulse.allow-sensitive=true
```

Guardas adicionales:

```bash
--label io.dockpulse.manage=false     # desactiva siempre
--label io.dockpulse.sensitive=true  # fuerza clasificación sensible
```

Las labels de un contenedor existente no se cambian en caliente de forma fiable; recrearlo con el contrato correcto y ejecutar refresh.

### Ignore y protect

Desde la UI se pueden aplicar por contenedor. En Compose, ignore/protect de stack actualiza todos los contenedores conocidos del mismo `agent_id + compose_project`. Una sincronización posterior conserva esas políticas aunque Docker recree el contenedor con otro ID.

## Qué hace un update

### Contenedor creado con `docker run`

El dry-run inspecciona el contrato y redacta valores de environment. La ejecución:

1. hace pull de la referencia actual;
2. detiene y renombra el contenedor anterior como candidato de rollback;
3. crea el reemplazo con el contrato reconstruido;
4. conecta redes adicionales;
5. arranca y verifica `State.Running=true`;
6. elimina el candidato de rollback.

Si falla después del rename, usa un contexto de recuperación independiente para eliminar el reemplazo, devolver el nombre al contenedor anterior y arrancarlo.

El agente rechaza de forma explícita contratos que no puede preservar de manera segura, entre ellos:

- `--rm`/auto-remove;
- publicación dinámica de todos los puertos;
- links Docker legacy;
- mounts de tipo desconocido;
- bindings sin puerto de host estable.

Revisar especialmente aliases o direcciones IP estáticas de red, dispositivos, límites de recursos, healthchecks y opciones de seguridad poco comunes. El MVP no garantiza fidelidad para todo el espacio de configuración de Docker; un dry-run exitoso es necesario, pero no reemplaza una prueba de aplicación.

Los datos persisten solo si estaban en bind mounts o volúmenes. El filesystem escribible del contenedor anterior no se migra.

### Docker Compose

Para scope `service`:

```text
docker compose --project-directory <dir> -p <project> pull <service>
docker compose --project-directory <dir> -p <project> up -d <service>
```

Para scope `stack`, omite el servicio y reconcilia el proyecto completo. Compose puede iniciar o reconciliar dependencias según la definición del proyecto. DockPulse no genera un rollback Compose automático; ante fallo, revisar `docker compose ps`, logs y el archivo Compose antes de intervenir.

## Jobs, locks y auditoría

Cada acción crea un job con:

- actor de `X-DockPulse-User`;
- agente, contenedor y target lock;
- action, timestamps y resultado;
- correlation ID y eventos estructurados.

Solo puede existir un job `queued` o `running` por contenedor; con scope stack el lock usa el proyecto completo. Un fallo de dispatch deja el job en `failed` y libera el lock.

El agente reintenta la entrega del estado terminal, pero no hay outbox durable ni reconciliación automática en el MVP. Si el control estuvo caído durante todo el final de una operación, el job puede quedar activo aunque Docker haya terminado. Antes de tocar la base:

1. consultar el job y sus eventos en el control plane;
2. revisar los logs JSON del agente y el estado real de Docker/Compose;
3. respaldar SQLite y detener el control plane;
4. solo entonces reconciliar el job offline o restaurar el backup.

No existe un endpoint administrativo de force-unlock en el MVP. No cambiar un job a `failed` sin verificar primero que no haya una ejecución activa.

## API de control plane

Base: `/api/v1`. La API humana depende del reverse proxy para autenticación. Estos ejemplos asumen localhost protegido.

```bash
DOCKPULSE_URL=http://127.0.0.1:8080
curl -fsS "$DOCKPULSE_URL/api/v1/agents"
curl -fsS "$DOCKPULSE_URL/api/v1/containers"
curl -fsS "$DOCKPULSE_URL/api/v1/jobs?limit=50"
```

Refresh:

```bash
curl -fsS -X POST \
  -H 'Content-Type: application/json' \
  -H 'X-DockPulse-User: operator@example' \
  -d '{}' \
  "$DOCKPULSE_URL/api/v1/agents/lxc-101/refresh"
```

Dry-run y update explícito:

```bash
CONTAINER_ID='lxc-101:docker-id'

curl -fsS \
  "$DOCKPULSE_URL/api/v1/containers/$CONTAINER_ID/preflight?scope=container"

curl -fsS -X POST \
  -H 'Content-Type: application/json' \
  -H 'X-DockPulse-User: operator@example' \
  -d '{"scope":"container"}' \
  "$DOCKPULSE_URL/api/v1/containers/$CONTAINER_ID/dry-run"

curl -fsS -X POST \
  -H 'Content-Type: application/json' \
  -H 'X-DockPulse-User: operator@example' \
  -d '{"scope":"container","confirm":true}' \
  "$DOCKPULSE_URL/api/v1/containers/$CONTAINER_ID/update"
```

Scopes válidos: `container` para Docker run; `service` o `stack` para Compose.

Endpoints principales:

| Método y path                                                       | Propósito                                                        |
| ------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `GET /health`                                                       | salud del control                                                |
| `POST /agents/register`                                             | bootstrap de agente                                              |
| `POST /agents/heartbeat`                                            | heartbeat HMAC del agente                                        |
| `GET /agents`, `GET /agents/:id`                                    | fleet                                                            |
| `DELETE /agents/:id?confirm=true`                                   | desregistro explícito; el historial referenciado puede impedirlo |
| `POST /agents/:id/refresh`                                          | refresh y evaluación                                             |
| `POST /agents/:id/self-update`                                      | self-update opt-in del Agent                                     |
| `GET /system`                                                       | versión actual, target y Agents                                  |
| `GET /agents/:id/containers`                                        | inventario por agente                                            |
| `GET /containers`, `GET /containers/:id`                            | inventario global/detalle                                        |
| `GET /containers/:id/preflight`                                      | checklist de elegibilidad por scope                              |
| `POST /containers/:id/dry-run`                                      | plan validado                                                    |
| `POST /containers/:id/update`                                       | update con confirmación                                          |
| `POST /containers/:id/ignore\|unignore\|protect\|unprotect`         | política individual                                              |
| `POST /stacks/:agent/:project/ignore\|unignore\|protect\|unprotect` | política de stack                                                |
| `GET /jobs`, `GET /jobs/:id`                                        | historial/detalle                                                |
| `GET /jobs/:id/logs`                                                | eventos persistidos JSON                                         |
| `GET /jobs/:id/events`                                              | stream SSE                                                       |

La API del agente expone `/health`, `/inventory`, `/heartbeat`, `/refresh`, `/dry-run`, `/update`, `/jobs/:id` y `/jobs/:id/logs`. Excepto `/health`, no está diseñada para uso humano directo: requiere firma HMAC completa y protección contra replay.

## Logs y diagnóstico

Server y Agents escriben JSON en stdout.

En el LXC Server:

```bash
docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  logs -f --tail=200 dockpulse-control
```

En cada LXC Agent containerizado:

```bash
docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  logs -f --tail=200 agent
```

En un LXC Agent instalado con systemd:

```bash
sudo journalctl -u dockpulse-agent -f -o cat
```

Para un diagnóstico inicial recopilar en el LXC correspondiente:

```bash
docker version
docker compose version
curl -i "$DOCKPULSE_URL/api/v1/health"
curl -fsS "$DOCKPULSE_URL/api/v1/agents"
curl -fsS "$DOCKPULSE_URL/api/v1/jobs?limit=20"
```

Revisar y redactar labels, URLs internas e IDs antes de compartir la salida.

## Troubleshooting

| Síntoma                              | Causa probable                                                                 | Comprobación/corrección                                                                       |
| ------------------------------------ | ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| Control no inicia                    | encryption key inválida                                                        | Debe decodificar a exactamente 32 bytes                                                       |
| `address already in use`             | puerto 8080/9090 ocupado                                                       | Cambiar `DOCKPULSE_CONTROL_PORT` o `DOCKPULSE_AGENT_PORT`                                     |
| Registro devuelve 401                | bootstrap distinto                                                             | Comparar control y agente; reiniciar agente después de corregir                               |
| `unknown agent` en heartbeat         | secret o registro desincronizado                                               | Confirmar bootstrap válido y re-registrar con el mismo agent ID                               |
| Agente offline                       | flujo agente→control o control→agente bloqueado                                | Probar ambas URLs desde los hosts correspondientes                                            |
| `permission denied` en socket        | usuario fuera del grupo/GID Docker                                             | Revisar `ls -l /var/run/docker.sock` y `docker ps` como usuario del agente                    |
| Compose no administrable             | working dir ausente, fuera de allow-list o path distinto dentro del contenedor | Inspeccionar labels y montar la misma ruta absoluta read-only                                 |
| Error de digest/401 de registry      | registry privado o rate limit                                                  | El MVP solo soporta acceso anónimo; el estado de update queda desconocido                     |
| Update rechazado como sensible       | falta opt-in                                                                   | Agregar `io.dockpulse.allow-sensitive=true` al recrear el workload, si el riesgo fue aceptado |
| Update rechazado por policy          | ignored/protected/read-only                                                    | Cambiar una sola guarda de forma explícita y volver a dry-run                                 |
| Job queda running                    | evento terminal no entregado                                                   | Verificar Docker y logs del Agent; respaldar antes de reconciliar offline                     |
| La app falla aunque el job succeeded | DockPulse solo verificó estado running                                         | Revisar logs/health de aplicación y hacer rollback operativo                                  |

## Backup y recuperación

La base vive en el volumen `dockpulse-data` y usa WAL. Para un backup consistente simple:

1. detener `dockpulse-control` de forma limpia;
2. respaldar el volumen completo, no solo `dockpulse.db` si quedan archivos WAL/SHM;
3. guardar por separado `deploy/control.env` o, como mínimo, la encryption key;
4. volver a iniciar y comprobar `/health`.

Con Proxmox, un snapshot/backup consistente del volumen o LXC con el control detenido es la opción preferida. Probar restauraciones periódicamente en una instancia aislada y arrancar inicialmente con agentes read-only.

La encryption key no tiene rotación online en el MVP. No reemplazarla sobre una base existente. Para rotar un agent secret, cambiarlo en el agente y reiniciarlo con un bootstrap token válido; el registro actualiza el secreto cifrado. La rotación del bootstrap debe coordinarse en el control y en **todos** los agentes, porque lo reutilizan al volver a registrarse tras una pérdida de conectividad.
