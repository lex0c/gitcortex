BINARY  := gitcortex
MODULE  := gitcortex
GOFLAGS := -trimpath

.PHONY: build test vet clean install

build:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/gitcortex/

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

install:
	go install $(GOFLAGS) ./cmd/gitcortex/

check: vet test
