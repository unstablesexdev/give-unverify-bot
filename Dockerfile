FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bot .

FROM alpine:3.20

WORKDIR /app

RUN adduser -D appuser
COPY --from=builder /app/bot /app/bot
COPY .env /app/.env

USER appuser

CMD ["/app/bot"]
