BINARY := fngbot

.PHONY: build test test-live lint run

# -s -w strip debug info, -trimpath removes local paths: a ~7 MB static binary.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

test:
	go test -race ./...

# Queries the real providers: checks that they still speak the protocol the bot expects.
test-live:
	FNG_LIVE_TEST=1 go test ./internal/alternative ./internal/coinglass -run Live -v -count=1

lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

run: build
	./$(BINARY) -config config.yaml
