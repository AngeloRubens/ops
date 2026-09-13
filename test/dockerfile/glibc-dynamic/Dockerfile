# The same program as go-musl, linked against glibc on a debian base: the other corner of the
# question, since alpine's libc is musl and nanos is written against what glibc asks of a kernel.
FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY hello.c /hello.c
RUN gcc -o /hello /hello.c

FROM debian:bookworm-slim
COPY --from=build /hello /usr/local/bin/hello
CMD ["hello"]
