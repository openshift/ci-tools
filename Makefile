build:
	go build ./cmd/...
.PHONY: build

install:
	go install ./cmd/...
.PHONY: install

test:
	go test ./...
.PHONY: test

lint:
	go vet ./...
.PHONY: lint