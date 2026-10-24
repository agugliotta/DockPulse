# DockPulse

DockPulse centraliza el inventario y las actualizaciones de contenedores Docker distribuidos entre hosts o LXCs de Proxmox. El control plane guarda estado, políticas, jobs y auditoría; cada `dockpulse-agent` solo accede al Docker daemon de su host local.

**Versión estable:** [v0.3.5](https://github.com/agugliotta/DockPulse/releases/tag/v0.3.5). Incluye correcciones de aislamiento entre agentes, actualizaciones Docker y Compose más seguras, mejoras de inventario y estado de jobs, y ajustes de interfaz. Consultar las [notas del release](https://github.com/agugliotta/DockPulse/releases/tag/v0.3.5) para el detalle.

> **Estado del MVP:** actualizaciones manuales con confirmación. El scheduler permanece deshabilitado hasta disponer de ventanas de mantenimiento y healthchecks de aplicación. La UI/API no implementa login propio: debe quedar en una red confiable o detrás de un reverse proxy autenticado.

## Capacidades

- Múltiples agentes con heartbeat y estados `healthy`, `degraded` y `offline`.
- Inventario de contenedores `docker run` y proyectos Docker Compose, aislado por agente.
- Comparación de digests con Docker Hub y Registry v2 anónimo.
- Dry-run y actualización por contenedor, servicio Compose o stack.
- Preflight por workload con checks de agent, scope, políticas, contrato y digest.
- Recreación conservadora de `docker run`, rollback ante fallos y preservación de mounts soportados, memoria compartida y configuración de logging.
- Opt-in, ignore/protect, clasificación sensible y modo read-only.
- Lock persistente por contenedor standalone o proyecto Compose, historial auditable y estado/logs de jobs vía SSE.
- Go, SQLite WAL con migraciones, SvelteKit y despliegue con Docker Compose.

## Antes de instalar

La topología requiere conectividad en ambos sentidos:

```text
operador -> DockPulse Web/API
agente   -> control plane (registro, heartbeat, inventario y eventos)
control  -> agente        (refresh, dry-run y update)
agente   -> Docker socket local y registries
```

El control plane **no** monta sockets Docker remotos. El agente sí tiene capacidad equivalente a root sobre su propio host mediante `/var/run/docker.sock`; se recomienda dedicar un agente por frontera Docker/LXC.

## Despliegue recomendado en Proxmox

DockPulse se instala en dos pasos separados:

1. **Server:** desplegar `deploy/docker-compose.control.yml` en un LXC Linux central. Aloja Web, API, SQLite y auditoría; no monta sockets Docker remotos.
2. **Agents:** desplegar `deploy/docker-compose.agent.yml` en cada LXC que ejecuta workloads Docker. Cada Agent monta únicamente el socket local y comienza en read-only.

La guía distingue cada comando y variable por rol:

- [Instalación y configuración](docs/installation.md)
- [Uso, operación y troubleshooting](docs/operations.md)
- [Arquitectura, seguridad y tradeoffs](docs/architecture.md)

El `docker-compose.yml` de la raíz levanta Server y Agent juntos. Está destinado a desarrollo o evaluación all-in-one, no es la topología recomendada para Proxmox.

Las instalaciones de Proxmox descargan imágenes privadas y versionadas desde GitHub Container Registry:

- `ghcr.io/agugliotta/dockpulse-control:<version>`
- `ghcr.io/agugliotta/dockpulse-agent:<version>`

Las imágenes publicadas son para `linux/amd64`. La descarga requiere autenticación en GHCR, como explica la [guía de instalación](docs/installation.md).

Las imágenes se publican al crear tags `vX.Y.Z`. También se publica `latest` para instalaciones que prefieren seguir el release más reciente:

- `DOCKPULSE_VERSION=0.3.5` fija explícitamente la versión desplegada y facilita volver a una imagen anterior.
- `DOCKPULSE_VERSION=latest` hace que `docker compose pull` resuelva el release más nuevo publicado.
- `DOCKPULSE_UPDATE_VERSION` indica en la UI/API a qué tag se intentará actualizar; por defecto se recomienda `latest`.

La página **System** muestra la versión actual del control plane, la versión reportada por cada agent y el target configurado.

## Actualizar a v0.3.5

Para instalaciones existentes, respaldar SQLite y conservar `DOCKPULSE_ENCRYPTION_KEY`. En cada host, configurar `DOCKPULSE_VERSION=0.3.5` en el archivo de entorno correspondiente; los archivos `.env.example` pueden apuntar a una versión anterior.

En el LXC Server, desde la carpeta de DockPulse:

```bash
docker compose --env-file deploy/control.env -f deploy/docker-compose.control.yml pull
docker compose --env-file deploy/control.env -f deploy/docker-compose.control.yml up -d
```

En cada LXC Agent, desde la carpeta de DockPulse:

```bash
docker compose --env-file deploy/agent.env -f deploy/docker-compose.agent.yml pull
docker compose --env-file deploy/agent.env -f deploy/docker-compose.agent.yml up -d
```

Verificar en **System** que Server y Agents reporten `0.3.5`, y comprobar heartbeat e inventario antes de actualizar workloads. Para self-update y recuperación, consultar la [guía de operación](docs/operations.md#versiones-y-self-update).

## Política de administración

La detección es amplia; la mutación es opt-in y conservadora:

| Workload                                | Condición mínima para actualizar                                                    |
| --------------------------------------- | ----------------------------------------------------------------------------------- |
| Docker Compose                          | Metadata completa y working directory accesible dentro de `DOCKPULSE_COMPOSE_ROOTS` |
| `docker run`                            | Label `io.dockpulse.manage=true` y contrato reconstruible                           |
| Base de datos/cache o workload sensible | Además, label `io.dockpulse.allow-sensitive=true`                                   |
| Cualquier workload                      | No estar ignored/protected y agente fuera de read-only                              |

`io.dockpulse.manage=false` siempre deshabilita la ejecución. `io.dockpulse.sensitive=true` fuerza la clasificación sensible.

Ejemplo de `docker run` administrable:

```bash
docker run -d --name whoami \
  --label io.dockpulse.manage=true \
  --restart unless-stopped \
  -p 8088:80 \
  traefik/whoami:v1.10
```

## Flujo operativo

1. En **Agents**, confirmar heartbeat e inventario.
2. Ejecutar **Refresh inventory** para consultar digests de registry.
3. Abrir el workload y revisar origen, política y razones de seguridad.
4. Revisar **Update readiness** y, opcionalmente, **Preview changes**; los valores de entorno se redactan.
5. Habilitar escrituras solo cuando corresponda y ejecutar **Update now** cuando el preflight lo permita.
6. Seguir cambios e inspecciones desde **Activity** y conservar el resultado para auditoría.

`update_available=false` no siempre significa “imagen actual”: si falta un digest o el registry no respondió, el estado es desconocido. Revisar `detection_method`, digests y `safety_reason`.

## Desarrollo y pruebas

El código requiere Go 1.25 o superior. El frontend requiere Node.js 22.

```bash
GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" go test ./...
cd web
npm ci
npm run check
npm run build
npx playwright install chromium
npm run test:e2e
```

También se puede ejecutar `make test` cuando las dependencias ya están instaladas.

Con un daemon Docker local disponible, ejecutar las pruebas de integración de actualizaciones, rollback y aislamiento de servicios Compose:

```bash
DOCKPULSE_DOCKER_TESTS=1 go test -race -count=1 -timeout 5m ./internal/agent -run TestDockerIntegration
```

Estas pruebas crean y eliminan recursos temporales de Docker y también se ejecutan en CI.

## Layout

```text
cmd/                    binarios control y agente
internal/agent/         discovery, registry, planner y executor
internal/control/       API central, dispatch y SSE
internal/store/         SQLite, cifrado y migraciones versionadas
web/                    DockPulse Web
deploy/                 Compose separados para Server/Agent y unidad systemd
docs/                   instalación, operación y arquitectura
.github/                CI, Dependabot y plantillas de colaboración
```

## Mantenimiento del repositorio

- [Guía de contribución](CONTRIBUTING.md)
- [Política de seguridad](SECURITY.md)
- CI para Go, SvelteKit, Playwright, Dockerfiles y Compose
- Dependabot semanal para Go/npm y mensual para GitHub Actions

## Límites explícitos del MVP

- Sin login/RBAC humano integrado: usar reverse proxy autenticado.
- Sin credenciales para registries privados.
- Sin rollback Compose automático ni validación de salud de aplicación.
- Sin scheduler activo, canary, HA o modo pull-only para agentes.
- El bootstrap token debe rotarse de forma coordinada en control y agentes; los agentes lo necesitan para volver a registrarse después de perder conectividad.
