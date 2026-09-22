# The nats server, in the image that carries no distribution at all, told through its command to
# publish the monitoring port its documentation describes. Its entrypoint is the binary itself.
FROM nats:2.11
EXPOSE 8222
CMD ["--http_port", "8222"]
