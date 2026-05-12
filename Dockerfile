FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o foundry ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates git
WORKDIR /app
COPY --from=builder /app/foundry .
COPY migrations/ migrations/
COPY web/ web/
COPY config.yaml .
EXPOSE 8080
CMD ["./foundry"]
