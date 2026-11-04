BINARY := logprint
LDFLAGS := -s -w

.PHONY: all build test clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)
