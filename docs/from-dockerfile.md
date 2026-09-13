# Building a package from a Dockerfile

A Dockerfile says how to assemble a file system and what to run out of it, which is most of what a
package is.

    ops pkg from-dockerfile ./Dockerfile --name myapp
    ops pkg load -l myapp -p 8080

The build runs through buildkit by way of the `docker` command, so multi-stage builds,
`COPY --chmod`, `RUN --mount` and heredocs all work as they do anywhere else. The file system of
the image becomes the package sysroot and the configuration the image carries becomes the manifest.

## What the image becomes

| In the Dockerfile | In the package |
|---|---|
| `RUN`, `COPY`, `ADD`, multi-stage | resolved at build time |
| `ENTRYPOINT` + `CMD` | `Program` and `Args` |
| `ENV` | `Env` |
| `WORKDIR` | `cwd` |
| `EXPOSE` | `RunConfig.Ports` |
| `USER`, `HEALTHCHECK`, `VOLUME` | ignored, with a warning |

The program keeps the place it has in the image rather than being copied to the root of the
package, so a runtime that finds its home from the path of its own binary still finds it. The file
system is made a quarter larger than the image plus a margin, because a server writes on its first
breath.

## When the image starts a script

Most published images do: tomcat runs `catalina.sh`, wildfly runs `standalone.sh`, and the official
node and temurin images wrap whatever you set in an entrypoint script of their own. A launcher
script ends in an exec of the program it was written to start, so the image can be asked what it
starts rather than what it declares, with one flag:

    ops pkg from-dockerfile ./Dockerfile --resolve-entrypoint --name wildfly

The image is started, the process tree is read once it has settled, and the program furthest from
the launcher is taken with the arguments the script worked out. The file system taken is the one
that run left behind, so an image that sets itself up on first boot - mysql initialises a data
directory - has that work in the package rather than still ahead of it.

## The images this was built against

Each of these is the image of a real thing, taken from the repository that publishes it and left
alone. The flags column is the whole of what was added to `ops pkg from-dockerfile ./Dockerfile`.

| image | flags | what comes out |
|---|---|---|
| prometheus | | a machine that answers `/-/healthy` |
| etcd | | a machine that serves `/version` and keeps its write ahead log |
| coredns | | a package, from a distroless base and a static binary |
| traefik | | a package |
| caddy | | a machine that serves its pages |
| dotnet, aspnet | | a machine that answers on 8080 |
| spring boot | | a machine that answers on 8080 |
| helidon | | a machine that answers `/greet` |
| python, uvicorn | | a machine that answers on 8000 |
| tomcat | `--resolve-entrypoint` | a machine that answers on 8080 |
| wildfly | `--resolve-entrypoint` | a machine that answers on 8080 |
| grafana | `--resolve-entrypoint` | a machine that answers `/login` |
| redis | `--resolve-entrypoint` | a machine that is ready to accept connections |
| node | `--resolve-entrypoint` | a machine that answers on 3000 |
| temurin, java | `--resolve-entrypoint` | a machine that answers on 8080 |
| mysql | `--resolve-entrypoint` | a package carrying the data directory its first start makes |
| keycloak | `--resolve-entrypoint` | a package carrying the server its first start builds |
| jenkins | `--resolve-entrypoint` | a package, tini and launcher seen through |
| nginx, apache httpd | `--resolve-entrypoint` | a package |

Every one of them is a scenario in `test/dockerfile`, built and run on each change:

    OPS=$PWD/ops ./test/dockerfile/run.sh tomcat-resolved

## Flags

| | |
|---|---|
| `[dockerfile]` | the file to build, `./Dockerfile` by default |
| `--context` | the build context, the directory of the Dockerfile by default |
| `--build-arg` | `name=value`, or `name` to take it from the environment |
| `--target` | the stage to build in a multi-stage Dockerfile |
| `--name`, `--version` | the name and version of the package |
| `--resolve-entrypoint` | run the image to see what it starts |
| `--resolve-timeout` | seconds to watch it for, 60 by default |
| `--keep-image` | keep the docker image the build produces |
