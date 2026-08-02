## Qué cambia

<!-- Resume el cambio y su impacto para usuarios u operadores. -->

## Por qué

<!-- Explica el problema, la decisión y los tradeoffs relevantes. -->

## Validación

- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] `npm run check` y `npm run build` en `web/`
- [ ] Flujo crítico de Playwright cuando aplica
- [ ] Configuración Compose validada cuando aplica

## Riesgo operativo

<!-- Incluye migraciones, compatibilidad, rollback y cambios de permisos. -->

- [ ] No incluye secretos ni datos sensibles.
- [ ] La documentación se actualizó si cambió el comportamiento operativo.
