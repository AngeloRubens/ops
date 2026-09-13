# Jenkins puts tini in front of its launcher - ENTRYPOINT ["/usr/bin/tini", "--",
# "/usr/local/bin/jenkins.sh"] - which is the shape the init peeling was written for, on an image
# people actually run rather than a corner case.
FROM jenkins/jenkins:lts-jdk21
ENV JAVA_OPTS="-Djenkins.install.runSetupWizard=false"
EXPOSE 8080
