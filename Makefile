BINARY    := mc-run
CMD       := ./cmd/$(BINARY)
PREFIX    ?= /usr/local
BINDIR    := $(PREFIX)/lib/helpers

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.version=$(VERSION)
GOFLAGS   := -trimpath

.PHONY: all build install uninstall clean

all: build

build: $(BINARY)

$(BINARY): $(shell find . -name '*.go' -not -path '*/vendor/*') go.mod
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) $(CMD)

install: $(BINARY)
	install -D -m 0755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

clean:
	rm -f $(BINARY)
