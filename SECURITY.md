# Política de seguridad

## Versiones soportadas

DockPulse se encuentra en fase MVP. Solo la rama `main` recibe correcciones de seguridad.

## Reportar una vulnerabilidad

No abras un issue público o compartido con detalles explotables, tokens, datos del homelab ni logs completos.

Usa **Security → Report a vulnerability** en GitHub para enviar un reporte privado. Incluye:

- versión o commit afectado;
- impacto y escenario de amenaza;
- pasos mínimos de reproducción;
- mitigaciones conocidas;
- evidencia redactada.

Si los private vulnerability reports no están habilitados, contacta al propietario del repositorio por un canal privado y comparte primero una descripción de alto nivel.

## Modelo de confianza del MVP

- La UI y API humanas no tienen login ni RBAC integrado; deben quedar detrás de un reverse proxy autenticado o en una red confiable.
- El Agent tiene control equivalente a root sobre el Docker daemon de su LXC. Debe desplegarse uno por frontera Docker y nunca exponer su puerto a Internet.
- Los secretos de Agent son individuales. El bootstrap token y la clave de cifrado deben generarse, almacenarse fuera del repositorio y rotarse de forma coordinada.
- El modo read-only debe permanecer activo hasta validar inventario, políticas, dry-run y conectividad.

Consulta [docs/architecture.md](docs/architecture.md) para el threat model y [docs/installation.md](docs/installation.md) para el despliegue seguro.
