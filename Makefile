.PHONY: build test run tidy docker-up docker-down

build:
	go build -o bin/m365backup ./cmd/server

test:
	go test ./...

run: build
	./bin/m365backup

tidy:
	go mod tidy

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
