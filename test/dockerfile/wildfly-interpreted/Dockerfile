# The same wildfly, told to interpret rather than compile. standalone.sh passes JAVA_OPTS through
# to the jvm, so this is the same machine with the jit taken out of it: if the server that stops
# with a broken regex node survives here, what is corrupting the jvm is the execution of compiled
# code - these runners have no kvm and everything is emulated - rather than the memory under it.
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
ENV JAVA_OPTS="-Xint -Xms64m -Xmx512m"

EXPOSE 8080

CMD ["/opt/jboss/wildfly/bin/standalone.sh", "-b", "0.0.0.0"]
