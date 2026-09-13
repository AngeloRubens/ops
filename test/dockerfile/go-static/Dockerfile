FROM golang:1.23-alpine AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -o /hello main.go

FROM scratch
COPY --from=build /hello /hello
CMD ["/hello"]
