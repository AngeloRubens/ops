# Building a Nanos image from a Dockerfile

`ops pkg from-dockerfile` turns a Dockerfile into an Ops package, and writes the Ops configuration
file that builds a Nanos image out of it.

    ops pkg from-dockerfile ./Dockerfile --name myapp --resolve-entrypoint
    ops pkg load -l myapp -p 8080

- [Why](#why)
- [The principle: carry what the program uses](#the-principle-carry-what-the-program-uses)
- [How it works](#how-it-works)
- [Example: Java, on the official Temurin images](#example-java-on-the-official-temurin-images)
- [Example: MySQL](#example-mysql)
- [Building the image again from the configuration file](#building-the-image-again-from-the-configuration-file)
- [When something is missing](#when-something-is-missing)
- [What the Dockerfile becomes](#what-the-dockerfile-becomes)
- [Flags](#flags)
- [The images this was built against](#the-images-this-was-built-against)

## Why

Most software is published as a container image, and a Dockerfile is where people already write down
how to assemble an application: which files it has, which environment it runs in, which command
starts it. A unikernel needs the same information, but it runs a single program and nothing else.
It has no shell, no package manager, no `init`, no users to log in, no graphical session.

A container image is built on top of a whole Linux distribution. Taking the image as it is would put
a hundred megabytes of tools in the unikernel that nothing will ever run, and it would hide what the
program really depends on. This command takes the Dockerfile as the description of the application
and leaves the distribution behind.

## The principle: carry what the program uses

The package carries what the program really needs to start and to keep running, and nothing else.

The way to know what a program needs is to watch it. A list of libraries read from the binary is
not enough: a JVM loads libraries by name, a database reads its configuration when it starts, a
server opens a certificate when the first client connects. So the image is run once in Docker, under
`strace`, for a short time (60 seconds by default), and every file the program and its children
opened is written down. That list is what the package keeps of the operating system:

- what the program opened is in the package, because it needed it;
- what it never opened is not, because nothing asked for it. That is the shell, `apt`, `systemd`,
  fonts on a server that draws nothing, documentation, the tools of the distribution.

What the image put there itself is the application and stays: a JDK unpacked in `/opt`, the jar,
the files the entrypoint script wrote on first boot. Which of those a program needs is the
Dockerfile's to say, not this command's.

A few rules follow from the principle:

- Only the program counts. A launcher script runs `sed`, `find`, a shell before it hands over to the
  real program. A unikernel runs no script, so what the launcher opened is not carried; only what
  the program, and the processes it makes, opened.
- The files that stand for the machine are written only when the program opened them: the
  processors in `/proc/cpuinfo` (see `--smp`) and the other few `/proc` and `/sys` files that
  programs such as MongoDB, CockroachDB, tcmalloc or NATS read to learn what machine they are on.
- The working directory and the home directory of the program are kept, because Nanos has to start
  the program there.
- Without `strace` (`--no-trace`, or an image that cannot be run) the command falls back to a more
  careful rule: it keeps what the program is linked against, which is more than it uses.

## How it works

1. **Build.** The Dockerfile is built with Docker (BuildKit), so multi-stage builds, `COPY --chmod`,
   `RUN --mount` and heredocs work as they do anywhere else.
2. **Run and watch** (`--resolve-entrypoint`). The image is started with `strace` in front of its
   entrypoint. When the process tree has settled, the program furthest from the launcher is taken,
   with the arguments and environment the script worked out, and the files it opened are read from
   the trace. The file system taken is the one that run left behind, so an image that sets itself up
   on first boot - MySQL initialises its data directory - has that work in the package.
3. **Prune.** The file system of the image becomes the sysroot of the package, minus the parts of
   the distribution the program never opened. The distribution's own account of what it installed
   (dpkg, apk or rpm) tells its files apart from the image's.
4. **Write the package and the configuration.** `package.manifest` goes in the package; the same
   configuration is written next to you as `<name>.config.json`.

Without `--resolve-entrypoint` the image is not run: `ENTRYPOINT` and `CMD` are read as written, which
is right for an image that starts its program directly. Most published images start a launcher
script instead (Tomcat runs `catalina.sh`, the official Node and Temurin images wrap the command in
an entrypoint script), and those need the flag.

## Example: Java, on the official Temurin images

The Eclipse Temurin images are the usual way to run Java in a container. Building on the official
JRE image gives a Nanos package that carries the JRE, the jar, and the few libraries the JVM opens
- not the Ubuntu the image sits on.

`Main.java`, a server that answers on 8080:

```java
import com.sun.net.httpserver.HttpServer;
import java.io.OutputStream;
import java.net.InetSocketAddress;

public class Main {
    public static void main(String[] args) throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress(8080), 0);
        server.createContext("/", exchange -> {
            byte[] body = "hello from java\n".getBytes();
            exchange.sendResponseHeaders(200, body.length);
            try (OutputStream out = exchange.getResponseBody()) {
                out.write(body);
            }
        });
        server.start();
    }
}
```

`Dockerfile`, a build stage on the JDK image and a run stage on the JRE image:

```dockerfile
FROM eclipse-temurin:25-jdk AS build
WORKDIR /src
COPY Main.java .
RUN javac Main.java && jar --create --file /app.jar --main-class Main Main.class

FROM eclipse-temurin:25-jre
COPY --from=build /app.jar /app.jar
CMD ["java", "-jar", "/app.jar"]
```

Build the package, and run it:

    ops pkg from-dockerfile ./Dockerfile --name hello-java --resolve-entrypoint
    ops pkg load -l hello-java -p 8080
    curl localhost:8080
    hello from java

The command prints what it did, in plain terms:

    the image docker built is 321 MB
    the file system is 197 MB: /opt 190MB /usr 6MB /etc 0MB
    the ops config is hello-java.config.json; build the image with: ops image create -c hello-java.config.json --package hello-java

The package is the JRE under `/opt/java/openjdk`, `app.jar`, `libc`, `libm`, `libdl`, `libpthread`,
`librt` and the dynamic linker, and four files of `/etc` that the JVM opens as it starts
(`passwd`, `nsswitch.conf`, `localtime`, `ld.so.cache`). It is the same set that the hand-made JRE
packages carry, plus those. There are no fonts, no `apt`, no `systemd`, no shell. `package.manifest`
and `hello-java.config.json` are the same file:

```json
{
  "Program": "/opt/java/openjdk/bin/java",
  "Args": ["java", "-jar", "/app.jar"],
  "Env": { "JAVA_HOME": "/opt/java/openjdk", "...": "..." }
}
```

### The Dockerfile that Adoptium publishes

The Dockerfiles Adoptium publishes (the `adoptium/containers` repository) build as they are. The
image they describe starts an entrypoint script (and, for the JDK image, `jshell`), so use
`--resolve-entrypoint`, and say what to run with your own `CMD` as above. Asked without the flag,
the command refuses an image whose entrypoint is a script, and says why, instead of making a
package that cannot start.

The same goes for Open Liberty: its official Dockerfile builds into a package that serves on its
port.

## Example: MySQL

MySQL is an image where watching the program matters. The official image starts `docker-entrypoint.sh`,
which initialises a data directory the first time, and then runs `mysqld`. Taking the Dockerfile
alone would produce a server that comes up to an empty data directory and stops.

```dockerfile
FROM mysql:8.4
ENV MYSQL_ALLOW_EMPTY_PASSWORD=1
CMD ["mysqld", "--user=root"]
```

`mysqld` will not run as root unless it is told to, and a Nanos kernel answers root to whoever asks,
so `--user=root` is how it is told.

    ops pkg from-dockerfile ./Dockerfile --name mysql --resolve-entrypoint --resolve-timeout 240
    ops pkg load -l mysql -p 3306

`--resolve-timeout 240` gives the image four minutes to initialise before the program is read; the
package then carries the initialised data directory, and the server on the console says
`ready for connections`.

## Building the image again from the configuration file

The command leaves two things: the package in `~/.ops/local_packages/<arch>/<name>/` and the
configuration file `<name>.config.json` in the current directory (`--config-out` says where else).
The file is the Ops configuration of the package, so it is the thing to edit when the machine
should be different: more memory, another port, another environment variable, a different number of
processors. Then build the image from it:

    ops image create -c hello-java.config.json --package hello-java -i hello-java

For instance, to give the Java server more room and a second port:

```json
{
  "Program": "/opt/java/openjdk/bin/java",
  "Args": ["java", "-Xmx512m", "-jar", "/app.jar"],
  "RunConfig": { "Ports": ["8080", "8081"], "Memory": "1g" }
}
```

Everything Ops can configure in a `config.json` can be set in that file, and the package it points to
supplies the file system. The scenarios `temurin-jre-resolved` and `mysql-resolved` in
`test/dockerfile` build their image this way on every change.

## When something is missing

The package carries what the program opened in the time it was watched. A file it opens later - a
certificate on the first connection, a plugin loaded after a minute - is not in the trace. There are
three ways to put it back:

- `--keep <path>` (repeatable) carries a file or a directory of the image whatever the program was
  seen to open: `--keep /etc/ssl`.
- `--resolve-timeout <seconds>` watches the program for longer.
- `--no-trace` goes back to the careful rule, which keeps everything the program is linked against.

A program that is not a JVM also keeps the certificates and time zones of the image, because it may
use them after the watching has stopped. A JVM carries its own.

The package is a directory, so a file can also be copied into its `sysroot` by hand, and the image
built again with `ops image create`.

## What the Dockerfile becomes

| In the Dockerfile | In the package |
|---|---|
| `RUN`, `COPY`, `ADD`, multi-stage | resolved at build time |
| `ENTRYPOINT` + `CMD` | `Program` and `Args` |
| `ENV` | `Env` |
| `WORKDIR` | `cwd` |
| `EXPOSE` | `RunConfig.Ports` |
| `USER`, `HEALTHCHECK`, `VOLUME` | ignored, with a warning |

Some details that are worth knowing:

- The program keeps the place it has in the image rather than being copied to the root of the
  package, so a runtime that finds its home from the path of its own binary still finds it.
- The file system is made a quarter larger than the image plus a margin, because a server writes on
  its first breath.
- A program the distribution installed itself - nginx, postgres, Apache - comes with its package
  whole, and the packages built from the same source, since it reads its modules, data and
  configuration by name.
- Images that keep their packages with rpm (ubi, Oracle Linux, Fedora) have the database read
  whether or not rpm is in the image. A database that cannot be read has the image carried whole,
  and says so.
- `--whole-image` carries the whole file system of the image, operating system and all.

## Flags

| | |
|---|---|
| `[dockerfile]` | the file to build, `./Dockerfile` by default |
| `--context` | the build context, the directory of the Dockerfile by default |
| `--build-arg` | `name=value`, or `name` to take it from the environment |
| `--target` | the stage to build in a multi-stage Dockerfile |
| `--name`, `--version` | the name and version of the package |
| `--resolve-entrypoint` | run the image under `strace` to see what it starts and which files it opens |
| `--resolve-timeout` | seconds to watch it for, 60 by default |
| `--no-trace` | with `--resolve-entrypoint`, do not watch the files it opens: keep what it is linked against |
| `--keep` | a file or directory of the image to carry whatever was opened (repeatable) |
| `--smp` | the number of processors the machine is run with, which `/proc/cpuinfo` lists and the configuration carries, 1 by default |
| `--config-out` | where to write the Ops configuration file, `<name>.config.json` by default |
| `--keep-image` | keep the Docker image the build produces |
| `--whole-image` | carry the whole file system of the image, operating system and all |

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
| temurin JRE, java | `--resolve-entrypoint` | a machine that answers on 8080 |
| open liberty | | a machine that answers on its port |
| mysql | `--resolve-entrypoint --resolve-timeout 240` | a machine that is ready for connections |
| tautulli | `--resolve-entrypoint` | a machine that puts its web server on 8181 |
| wordpress | `--resolve-entrypoint` | a machine whose apache is up and serving |
| nextcloud | `--resolve-entrypoint` | a machine whose apache is up and serving |
| memcached | `--resolve-entrypoint` | a machine that serves on 11211, on a kernel that reads `uid` from the manifest |

The shape of the Dockerfile is covered as well as the image: a static binary on `scratch`, a
dynamic one on `debian`, an `alpine` root file system, a `distroless` base, a multi stage build
driven by `--target` and `--build-arg`, the `# syntax=` directive, an `ENTRYPOINT` in shell form,
one whose command is built out of variables, and a `python` image.

memcached is the one row that is not a scenario here. It declines to start as root and a unikernel
has no one else to be, so it was run against a kernel that reads `uid` from the manifest and against
one that does not, to see which of the two it would talk to.

Every other one, image and shape alike, is a scenario in `test/dockerfile`, built and run on each
change:

    OPS=$PWD/ops ./test/dockerfile/run.sh temurin-jre-resolved
