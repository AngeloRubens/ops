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

## What the package carries

An image carries the distribution it was built on, and a unikernel carries what its program needs,
so the two are told apart by the distribution's own account of what it installed: dpkg's, the one
a distroless image keeps, alpine's, or rpm's.

- A file no package installed stays. It is what the image put there itself - a jdk unpacked under
  `/opt`, the application, what the launcher wrote on first boot - and what that is, a jdk or only
  a jre, is the Dockerfile's to say.
- Of what a package installed, what stays is what is linked against: by the program, by everything
  the image put there itself, and whatever its processes had mapped when the image was run with
  `--resolve-entrypoint`. A jdk takes libc, libdl, libgcc_s, libm, libpthread, librt, libstdc++,
  libz and the dynamic linker, and a program in C, Go or .NET the same way.
- A program the distribution installed itself - nginx, postgres, apache - comes with its package
  whole, and the packages built from the same source, since it reads its modules, data and
  configuration by name.
- What a program reads by name stays: `/etc/passwd`, `/etc/hosts`, `/etc/resolv.conf`,
  `/etc/nsswitch.conf` and their kin, the time zones, the certificates.
- Everything else the distribution installed is left out: the shell, the package manager, perl,
  the tools no one runs, since nanos starts one program and has no exec to start a second.

A library a program opens itself rather than links against - .NET opens ICU and OpenSSL, glibc
opens libgcc_s - is found by the name the program spells out, and stays the same way.

An image that keeps its packages with rpm - ubi, oracle linux, fedora - has its database read
whether or not rpm itself is in the image, as it is not in ubi-micro: sqlite, berkeley db and ndb
alike. A database that cannot be read has the image carried whole, and says so. `--whole-image`
carries any image whole.

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
Every row is a machine that booted and was asked for something, not a package that was only made.

| image | flags | what comes out |
|---|---|---|
| prometheus | | a machine that answers `/-/healthy` |
| etcd | | a machine that serves `/version` and keeps its write ahead log |
| influxdb | | a machine that answers on 8086 |
| caddy | | a machine that serves its pages |
| dotnet, aspnet | | a machine that answers on 8080 |
| spring boot | | a machine that answers on 8080 |
| helidon | | a machine that answers `/greet` |
| python, uvicorn | | a machine that answers on 8000 |
| tomcat | `--resolve-entrypoint` | a machine that answers on 8080 |
| wildfly | `--resolve-entrypoint` | a machine that answers on 8080 |
| grafana | `--resolve-entrypoint` | a machine that answers `/login` |
| redis | `--resolve-entrypoint` | a machine that answers on 6379 |
| node | `--resolve-entrypoint` | a machine that answers on 3000 |
| temurin, java | `--resolve-entrypoint` | a machine that answers on 8080 |
| tautulli | `--resolve-entrypoint` | a machine that puts its web server on 8181 |
| wordpress | `--resolve-entrypoint` | a machine whose apache is up and serving |
| nextcloud | `--resolve-entrypoint` | a machine whose apache is up and serving |
| memcached | `--resolve-entrypoint` | a machine that serves on 11211, on a kernel that reads `uid` from the manifest |

The shape of the Dockerfile is covered as well as the image: a static binary on `scratch`, a
dynamic one on `debian`, an `alpine` root file system, a `distroless` base, a multi stage build
driven by `--target` and `--build-arg`, the `# syntax=` directive, an `ENTRYPOINT` in shell form,
one whose command is built out of variables, and a `python` image. Nine of them, each a program
that starts and says its piece.

memcached is the one row that is not a scenario here. It declines to start as root and a
unikernel has no one else to be, so it was run against a kernel that reads `uid` from the
manifest and against one that does not, to see which of the two it would talk to.

Every other one, image and shape alike, is a scenario in `test/dockerfile`, built and run on
each change:

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
| `--whole-image` | carry the whole file system of the image, operating system and all |
