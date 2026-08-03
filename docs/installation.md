# Instalación en Proxmox: Server y Agents

Esta guía asume una instalación Linux sobre Proxmox VE:

- un LXC dedicado ejecuta **DockPulse Server**;
- cada LXC que ya ejecuta workloads Docker lleva su propio **DockPulse Agent**;
- el Server nunca monta los Docker sockets de los LXCs administrados.

El archivo `docker-compose.yml` de la raíz levanta Server y Agent juntos y se conserva para desarrollo. Para Proxmox se usan los archivos separados de `deploy/` descritos a continuación.

## 1. Entender los dos roles

| Rol        | Dónde se instala                        | Ejecuta                              | Acceso al Docker socket             |
| ---------- | --------------------------------------- | ------------------------------------ | ----------------------------------- |
| **Server** | Un LXC Linux central                    | Web, API, SQLite, jobs y auditoría   | No accede a sockets remotos         |
| **Agent**  | Cada LXC Linux que ejecuta contenedores | Discovery, dry-run y updates locales | Sí, únicamente al socket de ese LXC |

Ejemplo de red usado en esta guía:

| Componente               | Dirección de ejemplo |   Puerto |
| ------------------------ | -------------------- | -------: |
| DockPulse Server         | `10.10.0.10`         | 8080/TCP |
| Agent del Docker LXC 101 | `10.10.0.101`        | 9090/TCP |
| Agent del Docker LXC 102 | `10.10.0.102`        | 9090/TCP |

Reemplazar estas direcciones por las de la VLAN o red de gestión del homelab.

### Flujo de red necesario

- Cada Agent debe poder conectar al Server en 8080/TCP.
- El Server debe poder conectar a cada Agent en 9090/TCP.
- El navegador o reverse proxy debe poder conectar al Server.
- Cada Agent necesita salida HTTPS hacia los registries de imágenes.
- 9090/TCP debe aceptar tráfico solo desde la IP del Server.

## 2. Requisitos comunes en los LXCs

La guía presupone que Docker funciona dentro de cada LXC. La configuración de nesting, keyctl, AppArmor y almacenamiento para ejecutar Docker dentro de Proxmox depende del tipo de LXC y queda fuera de DockPulse.

En Debian o Ubuntu:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl openssl
docker version
docker compose version
```

Si la sesión del LXC ya es `root`, omitir `sudo` en los ejemplos. Si se usa un usuario no-root, debe tener permiso para ejecutar Docker.

### Autenticar Docker en GHCR

El Server y los Agents descargan imágenes privadas de GHCR; no compilan Go o SvelteKit durante la instalación. Todos los LXCs necesitan salida HTTPS a `ghcr.io`.

Crear en GitHub un único **personal access token (classic)** con alcance `read:packages`. Para este homelab puede compartirse entre todos los LXCs: simplifica la operación, aunque obliga a rotarlo en todos a la vez si se revoca o expira. No conceder `write:packages` ni guardar el token en los archivos `.env`.

Ejecutar una vez en el LXC Server y repetir en cada LXC Agent:

```bash
read -s GHCR_TOKEN
echo "$GHCR_TOKEN" | sudo docker login ghcr.io --username agugliotta --password-stdin
unset GHCR_TOKEN
sudo chmod 600 /root/.docker/config.json
```

Si Docker corre como usuario no-root, ejecutar el login con ese usuario y proteger su archivo `~/.docker/config.json`. Esta credencial sirve únicamente para descargar paquetes; no reemplaza el bootstrap token ni los secrets HMAC de DockPulse.

---

## Parte A — Instalar DockPulse Server

> **Ejecutar en:** el LXC Linux central elegido para DockPulse Server.

### A1. Copiar los archivos del Server

No hace falta clonar el repositorio privado dentro del LXC. Desde una estación de administración que tenga este repositorio:

```bash
ssh root@10.10.0.10 'install -d -m 0755 /opt/dockpulse/deploy'
scp deploy/docker-compose.control.yml deploy/control.env.example \
  root@10.10.0.10:/opt/dockpulse/deploy/
```

Luego, dentro del LXC Server:

```bash
cd /opt/dockpulse
```

Si no se usa SSH como `root`, copiar primero al home del usuario y mover los archivos con `sudo`. Reemplazar la IP de ejemplo por la real.

### A2. Crear la configuración del Server

```bash
cp deploy/control.env.example deploy/control.env
chmod 600 deploy/control.env
openssl rand -base64 32
openssl rand -hex 32
```

Editar `deploy/control.env` y asignar:

```dotenv
DOCKPULSE_VERSION=0.3.0
DOCKPULSE_UPDATE_VERSION=latest
DOCKPULSE_ENCRYPTION_KEY=<PRIMERA_SALIDA_BASE64>
DOCKPULSE_BOOTSTRAP_TOKEN=<SEGUNDA_SALIDA_HEX>
DOCKPULSE_CONTROL_PORT=8080
```

Responsabilidad de cada secreto:

- `DOCKPULSE_ENCRYPTION_KEY` cifra en SQLite los secrets de Agents. Guardar una copia segura fuera del LXC.
- `DOCKPULSE_BOOTSTRAP_TOKEN` permite registrar Agents. Los Agents deben usar el mismo valor.
- `DOCKPULSE_VERSION` puede quedar fijado a `0.3.0` o apuntar a `latest`.
- `DOCKPULSE_UPDATE_VERSION` es el target que muestra **System** para operaciones de actualización.

No reemplazar la encryption key sobre una base existente: el Server dejaría de poder descifrar los secrets almacenados.

### A3. Levantar solamente el Server

```bash
docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  config --quiet

docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  pull

docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  up -d
```

Este Compose crea únicamente:

- `dockpulse-control`;
- el volumen persistente `dockpulse-data`.

No crea un Agent ni monta `/var/run/docker.sock`.

### A4. Verificar el Server

En el LXC Server:

```bash
docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  ps

curl -fsS http://127.0.0.1:8080/api/v1/health
curl -fsS http://127.0.0.1:8080/api/v1/agents
```

Resultado esperado de health:

```json
{ "service": "dockpulse-control", "status": "ok" }
```

La lista de Agents estará vacía hasta completar la Parte B.

Desde la red de gestión, abrir `http://10.10.0.10:8080`, reemplazando la IP por la del LXC Server.

### A5. Permitir acceso de red

Permitir 8080/TCP desde:

- las IPs de los Agents;
- la red de gestión o el reverse proxy.

Si se usa `ufw`, un ejemplo restrictivo es:

```bash
sudo ufw allow from 10.10.0.0/24 to any port 8080 proto tcp
```

Adaptar la regla al firewall de Proxmox y a la red real. No publicar la UI/API directamente en Internet: el MVP delega login y RBAC humano a un reverse proxy autenticado.

---

## Parte B — Instalar un DockPulse Agent

> **Ejecutar en:** cada LXC Linux que contiene un Docker daemon y workloads locales.

Repetir toda esta parte por cada Docker LXC. No ejecutarla en el Server salvo que ese mismo LXC también sea, conscientemente, un host Docker administrado.

### B1. Verificar el Docker local

```bash
docker ps
docker compose version
ls -l /var/run/docker.sock
```

El Agent solo descubrirá y actualizará los contenedores mostrados por este Docker daemon.

### B2. Copiar los archivos del Agent

Desde la estación de administración:

```bash
ssh root@10.10.0.101 'install -d -m 0755 /opt/dockpulse/deploy'
scp deploy/docker-compose.agent.yml deploy/agent.env.example \
  root@10.10.0.101:/opt/dockpulse/deploy/
```

Luego, dentro de ese LXC Agent:

```bash
cd /opt/dockpulse
```

### B3. Crear la configuración exclusiva del Agent

```bash
cp deploy/agent.env.example deploy/agent.env
chmod 600 deploy/agent.env
openssl rand -hex 32
```

Editar `deploy/agent.env`. Ejemplo para el LXC `10.10.0.101`:

```dotenv
DOCKPULSE_VERSION=0.3.0
DOCKPULSE_UPDATE_VERSION=latest
DOCKPULSE_AGENT_ID=lxc-101
DOCKPULSE_AGENT_NAME=docker-lxc-101
DOCKPULSE_AGENT_URL=http://10.10.0.101:9090
DOCKPULSE_AGENT_SECRET=<SECRET_HEX_UNICO_DE_ESTE_AGENT>
DOCKPULSE_CONTROL_URL=http://10.10.0.10:8080
DOCKPULSE_BOOTSTRAP_TOKEN=<MISMO_TOKEN_CONFIGURADO_EN_EL_SERVER>
DOCKPULSE_READ_ONLY=true
DOCKPULSE_AGENT_LISTEN_ADDR=:9090
DOCKPULSE_AGENT_PORT=9090
DOCKPULSE_COMPOSE_ROOTS=/opt/stacks,/srv/compose
DOCKPULSE_COMPOSE_ROOT_1=/opt/stacks
DOCKPULSE_COMPOSE_ROOT_2=/srv/compose
DOCKPULSE_SELF_UPDATE_ENABLED=false
DOCKPULSE_SELF_UPDATE_DIR=/opt/dockpulse
DOCKPULSE_SELF_UPDATE_PROJECT=dockpulse-agent
DOCKPULSE_SELF_UPDATE_SERVICE=agent
```

Para que un Agent pueda actualizarse a sí mismo desde **System**, cambiar `DOCKPULSE_READ_ONLY=false` y `DOCKPULSE_SELF_UPDATE_ENABLED=true`. El Compose file debe estar disponible dentro del contenedor en `DOCKPULSE_SELF_UPDATE_DIR`; el ejemplo monta `/opt/dockpulse` en modo read-only, suficiente para ejecutar `docker compose pull` y `docker compose up -d`.

Para auto-actualizar el Server, instalar también un Agent en el LXC central del Server y configurar su self-update apuntando al Compose del control plane:

```dotenv
DOCKPULSE_SELF_UPDATE_ENABLED=true
DOCKPULSE_SELF_UPDATE_DIR=/opt/dockpulse
DOCKPULSE_SELF_UPDATE_PROJECT=dockpulse-control
DOCKPULSE_SELF_UPDATE_SERVICE=dockpulse-control
```

Ese Agent central es quien accede al Docker socket local del LXC Server; el control plane sigue sin montar sockets Docker.

Reglas de identidad:

- `DOCKPULSE_AGENT_ID` debe ser estable y único.
- `DOCKPULSE_AGENT_NAME` debe ser único y reconocible en la UI.
- Cada Agent debe tener su propio `DOCKPULSE_AGENT_SECRET`.
- El bootstrap token es compartido con el Server; el agent secret no.
- `DOCKPULSE_AGENT_URL` debe ser alcanzable desde el LXC Server.
- `DOCKPULSE_CONTROL_URL` debe ser alcanzable desde este LXC Agent.

### B4. Preparar los directorios Docker Compose

Docker guarda el working directory absoluto en la label `com.docker.compose.project.working_dir`. El mismo path debe existir dentro del contenedor `dockpulse-agent`.

Consultar un contenedor Compose existente:

```bash
CONTAINER_NAME=REPLACE_WITH_COMPOSE_CONTAINER_NAME
docker inspect "$CONTAINER_NAME" \
  --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}'
```

Si los proyectos están en `/opt/stacks` y `/srv/compose`:

```bash
sudo mkdir -p /opt/stacks /srv/compose
```

Mantener coordinadas estas variables:

```dotenv
DOCKPULSE_COMPOSE_ROOTS=/opt/stacks,/srv/compose
DOCKPULSE_COMPOSE_ROOT_1=/opt/stacks
DOCKPULSE_COMPOSE_ROOT_2=/srv/compose
```

El Compose del Agent monta ambas rutas read-only y en la misma ubicación absoluta. El Agent puede leer los archivos Compose y ejecuta las mutaciones a través del Docker socket local.

Si los stacks viven en otros paths, modificar las tres variables. La versión containerizada incluida admite dos roots montados; para más roots, ampliar `volumes` en `deploy/docker-compose.agent.yml` y agregarlos a `DOCKPULSE_COMPOSE_ROOTS`.

### B5. Levantar solamente el Agent

```bash
docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  config --quiet

docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  pull

docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  up -d
```

Este Compose crea un `dockpulse-agent`, publica 9090/TCP y monta únicamente el Docker socket de este LXC.

### B6. Verificar el Agent

Dentro del LXC Agent:

```bash
curl -fsS http://127.0.0.1:9090/health

docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  logs --tail=100 agent
```

Desde el LXC Server:

```bash
curl -fsS http://10.10.0.101:9090/health
curl -fsS http://127.0.0.1:8080/api/v1/agents
curl -fsS http://127.0.0.1:8080/api/v1/containers
```

El Agent debería aparecer `healthy` y enviar su inventario. Si `/health` responde desde el Agent pero no desde el Server, revisar routing y firewall de Proxmox.

### B7. Restringir el puerto del Agent

Permitir 9090/TCP únicamente desde la IP del Server. Ejemplo con `ufw`:

```bash
sudo ufw allow from 10.10.0.10 to any port 9090 proto tcp
```

`/health` es público dentro de esa red; el resto de la API del Agent requiere HMAC. No publicar 9090 hacia Internet.

### B8. Habilitar updates después de validar

Los Agents comienzan en read-only. Desde la UI:

1. confirmar inventario;
2. ejecutar refresh;
3. revisar al menos un dry-run;
4. proteger o ignorar workloads críticos.

Luego cambiar en `deploy/agent.env`:

```dotenv
DOCKPULSE_READ_ONLY=false
```

Recrear únicamente el Agent:

```bash
docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  up -d --force-recreate agent
```

El Server reflejará el nuevo modo en el siguiente heartbeat.

---

## Parte C — Añadir más Agents

Para `lxc-102`, repetir toda la Parte B y cambiar como mínimo:

```dotenv
DOCKPULSE_AGENT_ID=lxc-102
DOCKPULSE_AGENT_NAME=docker-lxc-102
DOCKPULSE_AGENT_URL=http://10.10.0.102:9090
DOCKPULSE_AGENT_SECRET=<OTRO_SECRET_UNICO>
```

No copiar `deploy/agent.env` con el mismo agent secret entre LXCs.

---

## Parte D — Alternativa: Agent como servicio systemd

Esta alternativa corresponde **solo al Agent**. El Server continúa usando su Compose y volumen SQLite.

Compilar el Agent con Go 1.25+ para Linux y la arquitectura del LXC:

```bash
go build -trimpath -o dockpulse-agent ./cmd/dockpulse-agent
sudo install -o root -g root -m 0755 dockpulse-agent /usr/local/bin/dockpulse-agent
```

Crear un usuario sin login y darle acceso al grupo del socket Docker:

```bash
sudo useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin dockpulse
sudo usermod -aG docker dockpulse
sudo install -d -o root -g dockpulse -m 0750 /etc/dockpulse
sudo install -o root -g dockpulse -m 0640 deploy/agent.env.example /etc/dockpulse/agent.env
sudo install -o root -g root -m 0644 deploy/dockpulse-agent.service /etc/systemd/system/dockpulse-agent.service
```

Editar `/etc/dockpulse/agent.env` con los valores de la Parte B. Las variables `DOCKPULSE_COMPOSE_ROOT_1/2` son exclusivas del Compose y pueden omitirse en systemd.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dockpulse-agent
sudo systemctl status dockpulse-agent
sudo journalctl -u dockpulse-agent -n 100 --no-pager
sudo -u dockpulse docker ps
```

Si el grupo del socket no se llama `docker`, ajustar `SupplementaryGroups` en la unidad y usar el GID mostrado por `ls -l /var/run/docker.sock`.

La unidad usa `ProtectHome=true`; ubicar stacks Compose en `/opt`, `/srv` u otra ruta accesible fuera de los homes, o revisar conscientemente esa restricción.

---

## Parte E — TLS y autenticación humana

Para una VLAN de gestión aislada se puede comenzar con HTTP y reglas de firewall restrictivas. Antes de cruzar redes no confiables:

- terminar TLS en Caddy, Traefik o Nginx;
- autenticar la Web/API con Authelia, oauth2-proxy u otro IdP;
- eliminar cualquier `X-DockPulse-User` enviado por el cliente y generarlo desde la identidad autenticada;
- desactivar buffering para `/api/v1/jobs/*/events`, que usa SSE;
- usar certificados confiables para el Go client del Server y los Agents.

El MVP no expone variables para instalar una CA privada. La CA debe agregarse al trust store del host o de la imagen correspondiente.

---

## Parte F — Actualizar DockPulse

### Server

Respaldar el volumen SQLite y conservar `DOCKPULSE_ENCRYPTION_KEY`. Elegir una versión publicada, cambiar `DOCKPULSE_VERSION` en `deploy/control.env` y luego ejecutar en el LXC Server:

```bash
cd /opt/dockpulse
docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  pull
docker compose \
  --env-file deploy/control.env \
  -f deploy/docker-compose.control.yml \
  up -d
```

Las migraciones SQLite se aplican al iniciar.

### Cada Agent

Actualizar primero en modo read-only o en un LXC no crítico. Cambiar `DOCKPULSE_VERSION` en `deploy/agent.env` a la misma versión seleccionada:

```bash
cd /opt/dockpulse
docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  pull
docker compose \
  --env-file deploy/agent.env \
  -f deploy/docker-compose.agent.yml \
  up -d
```

Verificar heartbeat, inventario y dry-run antes de continuar con el siguiente Agent.

---

## Referencia de configuración

### Variables del Server

| Variable                    | Requerida | Default          | Descripción                                     |
| --------------------------- | --------- | ---------------- | ----------------------------------------------- |
| `DOCKPULSE_VERSION`         | No        | `0.3.0`          | Tag inmutable de la imagen de DockPulse         |
| `DOCKPULSE_ENCRYPTION_KEY`  | Sí        | —                | Clave Base64 de 32 bytes para AES-256-GCM       |
| `DOCKPULSE_BOOTSTRAP_TOKEN` | Sí        | —                | Token compartido para registrar Agents          |
| `DOCKPULSE_LISTEN_ADDR`     | No        | `:8080`          | Bind interno del proceso                        |
| `DOCKPULSE_DB_PATH`         | No        | `./dockpulse.db` | Ruta SQLite; la imagen usa `/data/dockpulse.db` |
| `DOCKPULSE_WEB_DIR`         | No        | `./web/build`    | Build estático de DockPulse Web                 |
| `DOCKPULSE_CONTROL_PORT`    | Compose   | `8080`           | Puerto publicado por el LXC Server              |

### Variables del Agent

| Variable                      | Requerida | Default                    | Descripción                                 |
| ----------------------------- | --------- | -------------------------- | ------------------------------------------- |
| `DOCKPULSE_VERSION`           | No        | `0.3.0`                    | Tag inmutable de la imagen de DockPulse     |
| `DOCKPULSE_AGENT_ID`          | Sí        | —                          | ID estable y único del Docker LXC           |
| `DOCKPULSE_AGENT_NAME`        | Sí        | —                          | Nombre visible único                        |
| `DOCKPULSE_AGENT_URL`         | Sí        | —                          | URL alcanzable desde el Server              |
| `DOCKPULSE_AGENT_SECRET`      | Sí        | —                          | Secret HMAC exclusivo, mínimo 32 caracteres |
| `DOCKPULSE_CONTROL_URL`       | Sí        | —                          | URL del Server alcanzable desde el Agent    |
| `DOCKPULSE_BOOTSTRAP_TOKEN`   | Sí        | —                          | Mismo token configurado en el Server        |
| `DOCKPULSE_READ_ONLY`         | No        | `true`                     | Bloquea mutaciones en el Agent              |
| `DOCKPULSE_AGENT_LISTEN_ADDR` | No        | `:9090`                    | Bind interno del proceso                    |
| `DOCKPULSE_COMPOSE_ROOTS`     | No        | `/opt/stacks,/srv/compose` | Allow-list separada por comas               |
| `DOCKPULSE_DOCKER_BINARY`     | No        | `docker`                   | Ruta o nombre del Docker CLI                |
| `DOCKPULSE_AGENT_PORT`        | Compose   | `9090`                     | Puerto publicado por el LXC Agent           |

`DOCKPULSE_COMPOSE_ROOT_1/2` son variables del Compose del Agent para montar paths. No son leídas directamente por el binario.
