FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /portwatch ./cmd/portwatch

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 portwatch \
    && adduser -S -D -H -u 10001 -G portwatch portwatch \
    && mkdir /data \
    && chown portwatch:portwatch /data
WORKDIR /app
COPY --from=build /portwatch /usr/local/bin/portwatch
COPY config/sources.yaml /app/config/sources.yaml
ENV PORTWATCH_CONFIG=/app/config/sources.yaml PORTWATCH_DB=/data/portwatch.sqlite
USER portwatch
ENTRYPOINT ["/usr/local/bin/portwatch"]
