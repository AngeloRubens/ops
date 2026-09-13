# The same wildfly with its shared class archive turned off. A class data archive is mapped into
# the jvm and holds class metadata, and both failures seen so far - a regex node whose next is
# null, and a method handle with a parameter count of minus one and a bit - are corrupt class
# metadata. If the server survives without it, the archive is what is arriving wrong.
FROM eclipse-temurin:21-ubi9-minimal

RUN microdnf update -y && microdnf install --best --nodocs -y unzip && microdnf clean all

WORKDIR /opt/jboss

ENV WILDFLY_VERSION=41.0.1.Final
ENV JBOSS_HOME=/opt/jboss/wildfly

RUN cd $HOME \
    && curl -L -O https://github.com/wildfly/wildfly/releases/download/$WILDFLY_VERSION/wildfly-$WILDFLY_VERSION.zip \
    && unzip -q wildfly-$WILDFLY_VERSION.zip \
    && mv $HOME/wildfly-$WILDFLY_VERSION $JBOSS_HOME \
    && rm wildfly-$WILDFLY_VERSION.zip

ENV LAUNCH_JBOSS_IN_BACKGROUND=true
ENV JAVA_OPTS="-Xshare:off -Xms64m -Xmx512m"

EXPOSE 8080

CMD ["/opt/jboss/wildfly/bin/standalone.sh", "-b", "0.0.0.0"]
