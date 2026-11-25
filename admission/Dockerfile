ARG GO_IMAGE=golang:1.25-bookworm
ARG BASE_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS builder

ARG TARGETARCH=amd64
ARG VERSION=unknown
ARG REVISION=unknown
ARG GOPROXY

WORKDIR /src/sysrunc
COPY . .
ENV GOPROXY=${GOPROXY}
RUN cd admission && CGO_ENABLED=0 GOARCH="${TARGETARCH}" go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
    -o /tmp/sysrunc-admission ./cmd/sysrunc-admission

FROM ${BASE_IMAGE}

ARG DEBIAN_MIRROR=http://deb.debian.org/debian
ARG DEBIAN_SECURITY_MIRROR=http://deb.debian.org/debian-security
RUN sed -i \
    -e "s|http://deb.debian.org/debian-security|${DEBIAN_SECURITY_MIRROR}|g" \
    -e "s|http://deb.debian.org/debian|${DEBIAN_MIRROR}|g" \
    /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /tmp/sysrunc-admission /usr/local/bin/sysrunc-admission

ENTRYPOINT ["/usr/local/bin/sysrunc-admission"]
