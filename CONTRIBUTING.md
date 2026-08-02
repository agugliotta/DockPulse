# Contribuir a DockPulse

DockPulse controla actualizaciones de contenedores con acceso privilegiado al Docker daemon local. Los cambios deben priorizar seguridad, trazabilidad, compatibilidad y recuperación ante fallos.

## Flujo de trabajo

1. Crear una rama corta desde `main`.
2. Mantener la lógica de dominio fuera de la UI y encapsular toda interacción con Docker.
3. Agregar o actualizar tests para cada cambio de comportamiento.
4. Ejecutar los checks locales antes de abrir una pull request.
5. Documentar cambios operativos, migraciones y estrategia de rollback.

No incluir `.env`, tokens, credenciales de registry, bases SQLite, logs sin redactar ni datos reales del homelab.

## Checks locales

Backend y agente:

```bash
go test -race ./...
go vet ./...
go build ./cmd/...
```

Frontend:

```bash
cd web
npm ci
npm run format:check
npm run check
npm run build
npx playwright install chromium
npm run test:e2e
```

Artefactos de despliegue:

```bash
docker compose --env-file .env.example config --quiet
docker compose --env-file deploy/control.env.example \
  -f deploy/docker-compose.control.yml config --quiet
docker compose --env-file deploy/agent.env.example \
  -f deploy/docker-compose.agent.yml config --quiet
```

## Criterios para cambios sensibles

Una pull request que modifique autenticación, autorización, reconstrucción de contenedores, montaje de paths, ejecución Compose, migraciones o manejo de secretos debe incluir:

- casos negativos y validación de inputs;
- comportamiento ante timeout y agentes offline;
- impacto sobre read-only, ignored y protected;
- estrategia de rollback o una explicación explícita de por qué no es posible;
- logs útiles sin exposición de secretos.

Las vulnerabilidades no deben reportarse en issues. Seguir [SECURITY.md](SECURITY.md).
