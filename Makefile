.PHONY: test check build admin gateway

test:
	go test ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...

build:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/admin-web ./cmd/admin-web

admin:
	go run ./cmd/admin-web -f configs/admin-web.yaml

gateway:
	go run ./cmd/gateway -f configs/gateway.yaml
