.PHONY: test test-integration test-sdk sync-sdk check build admin gateway demo demo-down

test:
	go test ./...

# Needs BOTGUARD_TEST_MYSQL_DSN (and optionally BOTGUARD_TEST_REDIS_ADDR)
# pointing at disposable instances: every table is dropped. Packages share
# the database, so they must run one at a time (-p 1).
test-integration:
	test -n "$$BOTGUARD_TEST_MYSQL_DSN"
	go test -p 1 -count=1 ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...

build:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/admin-web ./cmd/admin-web
	go build -o bin/botguard-ctl ./cmd/botguard-ctl

admin:
	go run ./cmd/admin-web -f configs/admin-web.yaml

gateway:
	go run ./cmd/gateway -f configs/gateway.yaml

test-sdk:
	php packages/php/tests/run.php
	npm ci --prefix packages/browser --no-audit --no-fund
	npm test --prefix packages/browser

demo:
	examples/demo/start.sh

demo-down:
	docker compose -f examples/demo/docker-compose.yml down

# The gateway serves the browser widget at /sdk/v1/; refresh its copy.
sync-sdk:
	rm -f internal/sdkassets/v1/*.js
	cp packages/browser/src/*.js internal/sdkassets/v1/
