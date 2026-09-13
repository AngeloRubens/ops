FROM eclipse-temurin:21-jdk AS build
WORKDIR /src
COPY Main.java .
RUN javac Main.java && jar --create --file /app.jar --main-class Main Main.class

FROM eclipse-temurin:21-jre
COPY --from=build /app.jar /app.jar
CMD ["java", "-jar", "/app.jar"]
