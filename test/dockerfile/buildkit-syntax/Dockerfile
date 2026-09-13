# syntax=docker/dockerfile:1
# Not a real image: a probe for the dockerfile syntax that needs buildkit, which is what most
# Dockerfiles written in the last few years are made of.
FROM alpine:3.20
COPY --chmod=755 probe.sh /probe.sh
RUN --mount=type=cache,target=/tmp/cache echo cached > /tmp/cache/marker
RUN <<SCRIPT
echo "modern syntax works" > /etc/probe
SCRIPT
CMD ["/bin/busybox", "cat", "/etc/probe"]
