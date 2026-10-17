FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod main.go main_test.go ./
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /video-source-proxy .

FROM alpine:3.22
RUN adduser -D -H -s /sbin/nologin proxy
COPY --from=builder /video-source-proxy /usr/local/bin/video-source-proxy
USER proxy
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/video-source-proxy"]
