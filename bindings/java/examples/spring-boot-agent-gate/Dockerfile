# Build context: this folder. Works with docker, podman, and buildah bud.
FROM eclipse-temurin:17-jdk AS build
WORKDIR /src
COPY gradlew settings.gradle.kts build.gradle.kts ./
COPY gradle gradle
COPY src src
RUN ./gradlew --no-daemon -q bootJar -x test

FROM eclipse-temurin:17-jre
# Group 0 owns /app so OpenShift's random UID can still read it.
RUN useradd -r -u 10001 -g 0 agent
WORKDIR /app
COPY --from=build --chown=10001:0 /src/build/libs/spring-boot-agent-gate-1.0.0.jar app.jar
USER 10001
EXPOSE 8080
# Asks the app itself, over a plain TCP socket (the JRE image has no curl).
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/8080 && printf 'GET /actuator/health HTTP/1.0\\r\\n\\r\\n' >&3 && grep -q UP <&3"]
ENTRYPOINT ["java", "-jar", "app.jar"]
