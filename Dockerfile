FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ditto ./cmd/ditto

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /ditto /usr/local/bin/ditto

EXPOSE 8080
ENTRYPOINT ["ditto"]
CMD ["-config", "/etc/ditto/ditto.yaml"]
