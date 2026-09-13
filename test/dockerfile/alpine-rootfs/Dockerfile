# The binary from go-static, which runs, inside the file system from go-musl, which does not:
# whichever of the two the unikernel stops on is the one that carries the fault.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -o /hello main.go

FROM alpine:3.20
COPY --from=build /hello /hello
CMD ["/hello"]
