FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/zcode-server ./cmd/zcode-server

FROM alpine:3.22
RUN addgroup -S zcode && adduser -S -G zcode -h /var/lib/zcode-protect zcode
COPY --from=build /out/zcode-server /usr/local/bin/zcode-server
COPY configs/server.docker.json /etc/zcode-protect/server.json
RUN mkdir -p /var/lib/zcode-protect && chown -R zcode:zcode /var/lib/zcode-protect
USER zcode
EXPOSE 18081
ENTRYPOINT ["zcode-server"]
CMD ["--config", "/etc/zcode-protect/server.json"]
