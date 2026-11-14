FROM golang:bookworm AS builder

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=auto

WORKDIR /src

COPY backend-go/go.mod backend-go/go.sum ./
RUN go mod download

COPY backend-go/ ./
RUN go build -ldflags="-s -w" -o /out/agy-gateway ./cmd/agy-gateway

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    openssh-client \
    iputils-ping \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/agy-gateway /usr/local/bin/agy-gateway

EXPOSE 8088

CMD ["/usr/local/bin/agy-gateway"]
