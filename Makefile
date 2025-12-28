.PHONY: build install

build: 
	go build -o paprika-3-mcp ./cmd/...

install:
	go install ./cmd/paprika-3-mcp