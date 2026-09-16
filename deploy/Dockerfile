FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lucrum ./cmd/lucrum

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && mkdir /data \
    && chown app:app /data
COPY --from=build /lucrum /usr/local/bin/lucrum
USER app
WORKDIR /app
ENV DATA_DIR=/data
EXPOSE 3100
ENTRYPOINT ["/usr/local/bin/lucrum"]
