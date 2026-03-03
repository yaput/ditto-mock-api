.PHONY: build test lint run clean docker

BINARY := ditto
BUILD_DIR := .build

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/ditto

test:
	go test ./... -race -count=1

test-verbose:
	go test ./... -race -v -count=1

lint:
	go vet ./...

run: build
	$(BUILD_DIR)/$(BINARY) -config configs/ditto.yaml

clean:
	rm -rf $(BUILD_DIR) .ditto

docker:
	docker build -t ditto-mock-api .

tidy:
	go mod tidy

.DEFAULT_GOAL := build
