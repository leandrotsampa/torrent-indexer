FROM golang:1.24 AS builder

WORKDIR /go/src/app
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .

FROM alpine:latest
LABEL maintainer="Erickfb"
LABEL org.opencontainers.image.source="https://github.com/Erickfb/torrent-indexer"
LABEL org.opencontainers.image.description="Torrent indexer"
LABEL org.opencontainers.image.licenses="GPL-3.0"

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /go/src/app/app .

CMD ["/root/app"]
