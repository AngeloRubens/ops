# Dockerfile scenarios

Each directory here is one Dockerfile, whatever it needs, and an `expect` file saying what should
come of it. `run.sh` builds it into a package with `ops pkg from-dockerfile`, checks the manifest,
and boots the ones that should run.

    OPS=$PWD/ops ./test/dockerfile/run.sh tomcat-resolved

Sixteen of the scenarios are the image of a real thing, taken from the repository that publishes
it and left alone: tomcat, tomee, wildfly, glassfish, open liberty, payara, activemq, rocketmq,
mysql, apache, spring boot, helidon, quarkus, prometheus, coredns and traefik, plus the images
people pull most - nginx, redis, postgres, mongo, memcached, haproxy, rabbitmq, caddy and grafana.
The rest are small and exist to pin one behaviour each.

`expect` is read as shell, and holds:

| | |
|---|---|
| `DOCKERFILE` | the file to build, when it is not named Dockerfile |
| `EXPECT` | `ok`, `package` when the package is as far as the scenario goes, or `refuse` |
| `ERROR_MATCH` | what the refusal has to say |
| `OUTPUT_MATCH` | what the program has to print, or answer over its port |
| `PORT` / `URL_PATH` | for a program that serves rather than prints |
| `BOOT_WAIT` | two second turns to wait for the unikernel, 240 by default |
| `BUILD_FLAGS` | extra flags for `ops pkg from-dockerfile` |
| `MANIFEST_CHECK` | a jq filter over the manifest that has to hold |

A scenario built out of an artifact that has to be made first - a jar, a distribution tarball, a
repository of its own - carries a `prepare.sh` that makes it the way its own instructions say to.
