# ---- Build stage ----
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /build

# Cache module downloads separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /chart-sync \
    ./main.go

# ---- Runtime stage ----
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /chart-sync /chart-sync
COPY schema/ /schema/

ENTRYPOINT ["/chart-sync"]
