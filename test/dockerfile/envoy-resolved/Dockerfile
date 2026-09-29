# Envoy with a configuration of its own, the way its own quickstart writes one: a listener that
# answers directly, so that what is asked of it is answered by envoy rather than by something
# behind it. A c++ program behind an entrypoint script.
FROM envoyproxy/envoy:v1.31-latest
COPY envoy.yaml /etc/envoy/envoy.yaml
EXPOSE 10000
# Envoy keeps the state it would hand to its successor across a hot restart in a shared memory
# region, and opening one is fatal to it where there is no shared memory to open. There is no
# successor here - a unikernel is the one process it was made for - so it is told not to prepare
# for one, which is what its own documentation says this flag is for.
CMD ["envoy", "-c", "/etc/envoy/envoy.yaml", "--disable-hot-restart"]
