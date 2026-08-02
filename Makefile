.PHONY: test test-go test-web build compose-config
test: test-go test-web
test-go:
	go test ./...
test-web:
	cd web && npm run check && npm run build && npm run test:e2e
build:
	go build ./cmd/dockpulse-control ./cmd/dockpulse-agent
	cd web && npm run build
compose-config:
	docker compose config --quiet
