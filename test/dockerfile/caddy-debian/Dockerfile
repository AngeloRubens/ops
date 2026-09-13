# Caddy publishes only alpine images, so this is the same server built against a debian base: the
# question it answers is whether caddy itself is the trouble or the image it is shipped in.
FROM golang:1.23-bookworm AS build
RUN go install github.com/caddyserver/caddy/v2/cmd/caddy@v2.8.4

FROM debian:bookworm-slim
COPY --from=build /go/bin/caddy /usr/bin/caddy
COPY Caddyfile /etc/caddy/Caddyfile
COPY index.html /usr/share/caddy/index.html
EXPOSE 8080
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
