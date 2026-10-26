.PHONY: all build clean lint deps test integration-test snapshot linux-intel linux-intel-native

MAIN=./cmd
BIN=bin/
OUT=nri-db2
VERSION ?= dev

# IBM_DB_HOME: build-time clidriver 12.1.4 (headers and libs; /opt/ibm/clidriver in the builder image).
# RUNTIME_LIB: where the customer installs it; the binary's RUNPATH points there.
IBM_DB_HOME ?= /opt/ibm/clidriver
RUNTIME_LIB=/opt/ibm/clidriver/lib
CGO_ENV=CGO_ENABLED=1 IBM_DB_HOME=$(IBM_DB_HOME) CGO_CFLAGS=-I$(IBM_DB_HOME)/include CGO_LDFLAGS=-L$(IBM_DB_HOME)/lib
LDFLAGS=-r $(RUNTIME_LIB) -X main.version=$(VERSION)

BUILDER_IMAGE=nri-db2-builder:22.04
DOCKER_RUN=docker run --rm --platform=linux/amd64 -v "$(CURDIR)":/src -w /src \
	-v nri-db2-gocache:/root/.cache/go-build -v nri-db2-gomod:/go/pkg/mod
DB2_TEST_HOSTNAME ?= db2-121
DB2_TEST_PORT ?= 50000
MONUSER_PASSWORD ?= MonPass1

all: linux-intel
build: linux-intel

linux-intel:
	$(DOCKER_RUN) $(BUILDER_IMAGE) make linux-intel-native VERSION=$(VERSION)

linux-intel-native:
	$(CGO_ENV) GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN)/linux-amd64/$(OUT) $(MAIN)

test:
	$(DOCKER_RUN) $(BUILDER_IMAGE) go test -ldflags "-r $(RUNTIME_LIB)" ./...

integration-test:
	$(DOCKER_RUN) --network db2net -e DB2_TEST_HOSTNAME=$(DB2_TEST_HOSTNAME) \
		-e DB2_TEST_PORT=$(DB2_TEST_PORT) -e MONUSER_PASSWORD=$(MONUSER_PASSWORD) $(BUILDER_IMAGE) \
		go test -tags integration -count=1 -ldflags "-r $(RUNTIME_LIB)" ./...

clean:
	rm -rf $(BIN)

lint:
	$(DOCKER_RUN) $(BUILDER_IMAGE) golangci-lint run ./...

snapshot:
	$(DOCKER_RUN) $(BUILDER_IMAGE) goreleaser release --snapshot --clean

deps:
	$(DOCKER_RUN) $(BUILDER_IMAGE) sh -c 'go mod download && go mod tidy'

# Windows amd64: no cgo, so the build needs neither clidriver nor a C toolchain. The binary loads
# db2cli64.dll at run time.
.PHONY: windows-amd64 windows-amd64-native vet-windows lint-windows

windows-amd64:
	$(DOCKER_RUN) $(BUILDER_IMAGE) make windows-amd64-native VERSION=$(VERSION)

windows-amd64-native:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN)/windows-amd64/$(OUT).exe $(MAIN)

vet-windows:
	$(DOCKER_RUN) -e CGO_ENABLED=0 -e GOOS=windows -e GOARCH=amd64 $(BUILDER_IMAGE) go vet -tags integration ./...

lint-windows:
	$(DOCKER_RUN) -e CGO_ENABLED=0 -e GOOS=windows -e GOARCH=amd64 $(BUILDER_IMAGE) golangci-lint run ./...
