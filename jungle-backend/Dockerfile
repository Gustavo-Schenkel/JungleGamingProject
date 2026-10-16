FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOMAXPROCS=2 go build -p 2 -o /out/api ./cmd/api
FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -H appuser
WORKDIR /app
COPY --from=build /out/api /app/api
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/api"]
