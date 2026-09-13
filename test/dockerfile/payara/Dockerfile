# The dockerfile payara publishes is filtered by maven before it is built - its base image is
# written @docker.java.image@ - so what is used here is the image that comes out of it, which is
# also how anyone deploying an application to payara writes their own dockerfile. The entrypoint
# and command are the ones payara sets:
#
#   ENTRYPOINT ["tini", "--", "/opt/payara/scripts/entrypoint.sh"]
#
# https://github.com/payara/Payara/blob/main/appserver/extras/docker-images/server-full/src/main/docker/Dockerfile
FROM payara/server-full:6.2025.1-jdk21
COPY hello.txt /opt/payara/deployments/hello.txt
