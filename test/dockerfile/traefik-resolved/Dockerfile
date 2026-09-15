# The official traefik image, configured the way its documentation configures one - through the
# environment - to answer a ping on 8080, since 80 is not a port a hosted runner can forward.
FROM traefik:v3.7
ENV TRAEFIK_ENTRYPOINTS_WEB_ADDRESS=:8080
ENV TRAEFIK_PING=true
ENV TRAEFIK_PING_ENTRYPOINT=web
EXPOSE 8080
