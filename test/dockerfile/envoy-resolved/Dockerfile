# Envoy with a configuration of its own, the way its own quickstart writes one: a listener that
# answers directly, so that what is asked of it is answered by envoy rather than by something
# behind it. A c++ program behind an entrypoint script.
FROM envoyproxy/envoy:v1.31-latest
COPY envoy.yaml /etc/envoy/envoy.yaml
EXPOSE 10000
