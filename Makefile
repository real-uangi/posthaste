.PHONY: frontend test sdk-test check build

frontend:
	pnpm --dir web build

test: frontend
	go test ./...

sdk-test:
	cd sdk/go && GOWORK=off go test ./... && GOWORK=off go test -race ./... && GOWORK=off go vet ./...

check: frontend
	pnpm --dir web test
	go test -race ./...
	go vet ./...
	$(MAKE) sdk-test

build: frontend
	go build -o bin/posthaste .
