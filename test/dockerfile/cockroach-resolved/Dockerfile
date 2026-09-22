# Cockroach as its own quickstart starts it: a single insecure node, which is the only shape that
# comes up without a cluster to join. Its entrypoint is a script, so it is asked what it starts.
FROM cockroachdb/cockroach:v24.2.3
EXPOSE 8080
CMD ["start-single-node", "--insecure", "--http-addr=0.0.0.0:8080"]
