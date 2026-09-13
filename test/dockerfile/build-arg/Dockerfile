FROM alpine:3.20 AS base
RUN mkdir -p /etc/greeting

FROM base AS other
CMD ["/bin/busybox", "echo", "the wrong stage was built"]

FROM base AS runtime
ARG GREETING=unset
RUN echo "$GREETING from the build argument" > /etc/greeting/text
CMD ["/bin/busybox", "cat", "/etc/greeting/text"]
