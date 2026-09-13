# The dockerfile the spring boot reference documentation gives for a layered jar, unchanged:
# https://docs.spring.io/spring-boot/reference/packaging/container-images/dockerfiles.html
FROM bellsoft/liberica-openjre-debian:25-cds AS builder
WORKDIR /builder

# This points to the built jar file in the target folder
# Adjust this to 'build/libs/*.jar' if you're using Gradle
ARG JAR_FILE=target/*.jar

# Copy the jar file to the working directory and rename it to application.jar
COPY ${JAR_FILE} application.jar

# Extract the jar file using an efficient layout
RUN java -Djarmode=tools -jar application.jar extract --layers --destination extracted

# This is the runtime container
FROM bellsoft/liberica-openjre-debian:25-cds
WORKDIR /application

COPY --from=builder /builder/extracted/dependencies/ ./
COPY --from=builder /builder/extracted/spring-boot-loader/ ./
COPY --from=builder /builder/extracted/snapshot-dependencies/ ./
COPY --from=builder /builder/extracted/application/ ./

EXPOSE 8080
ENTRYPOINT ["java", "-jar", "application.jar"]
