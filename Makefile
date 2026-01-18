.PHONY: all build test clean proto

VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -X main.Version=$(VERSION)

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/api-server ./cmd/api-server
	go build -ldflags "$(LDFLAGS)" -o bin/controller ./cmd/controller
	go build -ldflags "$(LDFLAGS)" -o bin/scheduler ./cmd/scheduler
	go build -ldflags "$(LDFLAGS)" -o bin/executor ./cmd/executor
	go build -ldflags "$(LDFLAGS)" -o bin/ffctl ./cmd/cli

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		pkg/api/grpc/proto/*.proto

test:
	go test -v -race -cover ./...

test-integration:
	go test -v -tags=integration ./...

lint:
	golangci-lint run ./...

migrate:
	migrate -path migrations/postgres -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations/postgres -database "$(DATABASE_URL)" down 1

dev-env-up:
	docker-compose -f docker-compose.dev.yml up -d

dev-env-down:
	docker-compose -f docker-compose.dev.yml down

clean:
	rm -rf bin/
