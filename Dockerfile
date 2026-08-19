# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/pgmcp ./cmd/pgmcp

FROM gcr.io/distroless/static-debian12:nonroot
LABEL io.modelcontextprotocol.server.name="io.github.pascalallen/pgmcp"
COPY --from=builder /out/pgmcp /pgmcp
ENTRYPOINT ["/pgmcp"]
