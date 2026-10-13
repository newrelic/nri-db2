# Cgo build image for nri-db2 (linux/amd64 only): Ubuntu 22.04 (glibc 2.35) + clidriver 12.1.4.
#
# clidriver 12.1.4 needs glibc >= 2.34 to link, so the project builds only on this baseline.
# clidriver 11.5.9 does not verify the TLS server hostname, so it is not used.
#
# Build the image (from the repo root):
#   docker build --platform=linux/amd64 -t nri-db2-builder:22.04 build/builder
#
# Build nri-db2 (from the repo root): make linux-intel
FROM --platform=linux/amd64 ubuntu:22.04

ARG GO_VERSION=1.24.5
ARG CLIDRIVER_VERSION=12.1.4
ARG CLIDRIVER_URL=https://public.dhe.ibm.com/ibmdl/export/pub/software/data/db2/drivers/odbc_cli/v${CLIDRIVER_VERSION}/linuxx64_odbc_cli.tar.gz
# sha256 of the 12.1.4 tarball (linuxx64_odbc_cli.tar.gz). Change it together with CLIDRIVER_VERSION.
ARG CLIDRIVER_SHA256=77b754d19be2cd5c55f6feb8bd951c2b17aa6646289944855612c5a2ac555f55
ARG GORELEASER_VERSION=2.11.2
ARG GOLANGCI_LINT_VERSION=2.8.0

RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
      gcc libc6-dev curl ca-certificates git libxml2 libpam0g make binutils \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz

# Full clidriver, including include/ (needed by cgo at build time). The tarball is verified before it is unpacked.
RUN mkdir -p /opt/ibm \
 && curl -fsSL "${CLIDRIVER_URL}" -o /tmp/clidriver.tar.gz \
 && echo "${CLIDRIVER_SHA256}  /tmp/clidriver.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/clidriver.tar.gz -C /opt/ibm \
 && rm /tmp/clidriver.tar.gz \
 && test -d /opt/ibm/clidriver/include \
 && test -e /opt/ibm/clidriver/lib/libdb2.so.1

ENV PATH=/usr/local/go/bin:/go/bin:${PATH} \
    GOPATH=/go \
    GOFLAGS=-buildvcs=false \
    CGO_ENABLED=1 \
    IBM_DB_HOME=/opt/ibm/clidriver \
    CGO_CFLAGS=-I/opt/ibm/clidriver/include \
    CGO_LDFLAGS=-L/opt/ibm/clidriver/lib

RUN GOBIN=/go/bin go install github.com/goreleaser/goreleaser/v2@v${GORELEASER_VERSION} \
 && GOBIN=/go/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}

RUN git config --global --add safe.directory '*'

WORKDIR /src
CMD ["go", "version"]
