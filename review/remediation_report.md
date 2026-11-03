# Correcciones y verificación — 2026-11-03

Los ocho hallazgos del informe original quedaron atendidos en el código y la documentación locales. No se desplegaron servicios ni se creó un release.

| Hallazgo | Corrección | Verificación |
| --- | --- | --- |
| 1. Pérdida de políticas | Se conservan los registros ausentes con decisiones centrales o historial. Los candidatos `.dockpulse-backup` quedan fuera del inventario y de la reconciliación de identidad. | Regresiones de ausencia por cada política y de backup/reemplazo en ambos órdenes; preservan ID lógico, bloqueo e historial. |
| 2. Mutaciones simultáneas | El agente admite una mutación a la vez, incluido self-update. Repetir el mismo job y solicitud devuelve el job existente; reutilizarlo con otra solicitud se rechaza. El control solo libera el lock ante rechazo 4xx explícito; timeout, 5xx, JSON inválido o falta de job ID conservan el lock. | Regresiones de concurrencia, idempotencia, solicitud cambiada, timeout y respuestas 409/500/200 inválida/202 incompleta. |
| 3. Réplicas protegidas | Preflight y despacho validan todos los miembros del servicio Compose seleccionado, además de conservar la validación de stack completo. | La réplica protegida bloquea preflight y despacho, sin llamar al agente. |
| 4. Contexto Compose | Se recuperan env-files declarados por las labels y se contrasta el hash resuelto con el desplegado. Un contexto distinto, hash ausente o servicio nuevo sin contrato verificable bloquea la ejecución. El cálculo de hash activa profiles para poder verificar esos servicios. | Prueba con Compose CLI real, custom.env y servicio con profile: contexto original aceptado; archivo perdido, modificado y hash ausente rechazados. Integración Docker con servicio de profile y rechazo de cambios de configuración. |
| 5. Referencias fijadas | El parser conserva el digest al elegir el manifest remoto, también con tag más digest. | Se comprueba la URI solicitada para ambos formatos; nunca usa latest. |
| 6. Bootstrap | Re-registrar una identidad exige el mismo secreto individual existente. La comprobación y escritura son una transacción; otro bootstrap holder no puede reemplazar secreto, URL ni modo de esa identidad. | Se rechaza otro secreto y se verifican los valores intactos; la re-inscripción legítima funciona. |
| 7. Acceso humano | Arquitectura y diagramas explicitan autenticación externa y responsabilidad del proxy sobre X-DockPulse-User. | Fuente Mermaid y SVG coinciden; enlaces y XML válidos. |
| 8. Garantías operativas | Se documentan rollback dependiente del proceso, entrega de eventos sin durabilidad, políticas centrales evaluadas al despachar y recuperación manual tras interrupción. Se aclaran también registry privado y el motivo de usar SSE. | Inspección contra las rutas reales de ejecución, entrega y recuperación. |

## Checks completados

- `go test -race ./...`: aprobado, con nuevas regresiones.
- `DOCKPULSE_DOCKER_TESTS=1 go test -race -count=1 -timeout 5m ./internal/agent -run TestDockerIntegration`: aprobado contra Docker local. Se verificaron actualización y rollback standalone, propagación de mounts y aislamiento de un servicio Compose con profile, usando recursos temporales aislados del propio test.
- `go vet ./...` y `go build ./cmd/...`: aprobados.
- Los tres Compose documentados pasan `config --quiet`.
- `git diff --check`: aprobado.

La primera ejecución de integración detectó que Compose no calcula el hash de un servicio con profile deshabilitado. Se corrigió activando los profiles únicamente durante esa inspección; la suite completa de integración pasó después del ajuste.

## Cambios de operación y límites

- El control y los agentes necesitan desplegar los binarios nuevos para contar con todas las guardas. No hay migración de esquema ni dependencias nuevas.
- Las actualizaciones de workloads Compose quedan limitadas al contrato de configuración desplegado. Los cambios de configuración intencionales se aplican por separado con Compose; después se refresca inventario. El hash puede bloquear una operación si no se puede reproducir su contexto original, aunque el YAML sea válido.
- La re-inscripción ya no sirve para reemplazar silenciosamente un agent secret. El MVP no incorpora rotación online de esos secretos; las guías explican el rechazo y la recuperación. Bootstrap sigue permitiendo crear identidades nuevas.
- Un dispatch incierto mantiene el lock hasta confirmar el resultado o reconciliarlo manualmente. La deduplicación del agente es local al proceso y a su historial acotado; no es un journal durable.
- Las políticas perdidas antes de la corrección no se pueden reconstruir automáticamente. Deben revisarse en el manager.
- No se añadieron login, scheduler, outbox ni recuperación automática tras caída del proceso. Esos límites siguen explícitos.

El frontend no cambió. No se repitieron sus checks de formato/build/Playwright en esta corrección; habían pasado en la revisión inicial. Tampoco se comprobó la configuración de proxies/firewalls de hosts desplegados ni se ejecutó self-update sobre un agente real.

Las pruebas de producto están en `internal/agent/review_regression_test.go`, `internal/control/review_regression_test.go` e `internal/store/review_regression_test.go`. Se pueden ejecutar junto con el resto mediante `python3 review/evidence/run.py`; este comando no habilita las pruebas que mutan Docker.
