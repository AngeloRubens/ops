# The upstream wildfly image, unchanged, from https://github.com/wildfly/wildfly-container
ARG jdk=21
ARG dist=ubi9-minimal
FROM eclipse-temurin:${jdk}-${dist}

RUN microdnf update -y && \
    microdnf install --best --nodocs -y unzip && \
    microdnf clean all

WORKDIR /opt/jboss

ENV WILDFLY_VERSION=41.0.1.Final
ARG variant=wildfly
ENV WILDFLY_DIST=${variant}-${WILDFLY_VERSION}
ENV JBOSS_HOME=/opt/jboss/wildfly

RUN cd $HOME \
    && curl -L -O https://github.com/wildfly/wildfly/releases/download/$WILDFLY_VERSION/$WILDFLY_DIST.zip \
    && unzip -q $WILDFLY_DIST.zip \
    && mv $HOME/$WILDFLY_DIST $JBOSS_HOME \
    && rm $WILDFLY_DIST.zip

ENV LAUNCH_JBOSS_IN_BACKGROUND=true

EXPOSE 8080

CMD ["/opt/jboss/wildfly/bin/standalone.sh", "-b", "0.0.0.0"]
