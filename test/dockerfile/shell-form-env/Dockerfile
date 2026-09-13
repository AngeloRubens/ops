# The shape every dubbo sample uses - 'ENTRYPOINT exec java $JAVA_OPTS -jar app.jar' - on a base
# that still exists: the sample's own dockerfiles are built on openjdk images, which have been
# removed from docker hub along with the deprecated official image.
FROM eclipse-temurin:21-jdk AS build
WORKDIR /src
COPY Main.java .
RUN javac Main.java && jar --create --file /app.jar --main-class Main Main.class

FROM eclipse-temurin:21-jre
COPY --from=build /app.jar /app.jar
ENV JAVA_OPTS=-Xmx256m
EXPOSE 8080
ENTRYPOINT exec java $JAVA_OPTS -jar /app.jar
