FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lucrum .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 lucrum \
    && adduser -S -D -H -u 10001 -G lucrum lucrum \
    && mkdir /data && chown lucrum:lucrum /data
COPY --from=build /out/lucrum /usr/local/bin/lucrum
USER 10001:10001
ENV DATA_DIR=/data HTTP_ADDR=:8080
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/lucrum"]
