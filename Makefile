.PHONY: build install test vet

install:
	go install .

build:
	go build -o sx .

test:
	go test ./...

vet:
	go vet ./...
