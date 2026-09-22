# Bun, serving with its own http server. The image puts an entrypoint script in front of the
# runtime, so what it starts has to be asked rather than read.
FROM oven/bun:1.2-debian
RUN printf '%s\n' 'Bun.serve({ port: 8080, fetch: () => new Response("bun is serving\n") });' > /server.js
EXPOSE 8080
CMD ["bun", "run", "/server.js"]
