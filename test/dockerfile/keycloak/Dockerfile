# Keycloak builds itself the first time it starts, which is the hardest thing to ask of a package:
# what the image carries is not yet a server. Its entrypoint is kc.sh.
FROM quay.io/keycloak/keycloak:26.0
ENV KC_BOOTSTRAP_ADMIN_USERNAME=admin
ENV KC_BOOTSTRAP_ADMIN_PASSWORD=admin
ENV KC_HTTP_ENABLED=true
ENV KC_HOSTNAME_STRICT=false
EXPOSE 8080
CMD ["start-dev"]
