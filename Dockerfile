# SPDX-License-Identifier: AGPL-3.0-or-later
FROM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY schema/ ./schema/
COPY web/embed.go ./web/embed.go
COPY --from=web-builder /src/web/dist ./web/dist
# Value-free so BuildKit's automatic platform values apply; empty (legacy builder) builds natively.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -tags embedui -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/netplane ./cmd/netplane

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=go-builder /out/netplane /usr/local/bin/netplane
COPY ha-addon/run.sh /run.sh
RUN chmod +x /run.sh
VOLUME ["/data"]
EXPOSE 8099
ENTRYPOINT ["/run.sh"]
