# Deno is published as a bare binary meant to be copied into an image of one's own, which is the
# multi stage shape a great many Dockerfiles have.
FROM denoland/deno:bin AS deno
FROM debian:12-slim
COPY --from=deno /deno /usr/local/bin/deno
RUN printf '%s\n' 'Deno.serve({ port: 8080 }, () => new Response("deno is serving\n"));' > /server.js
EXPOSE 8080
CMD ["deno", "run", "--allow-net", "/server.js"]
