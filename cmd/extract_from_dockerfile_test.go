package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	dockerContainer "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
)

func TestEntrypointArgvAppendsTheCommandToTheEntrypoint(t *testing.T) {
	argv, err := entrypointArgv([]string{"/usr/bin/java"}, []string{"-jar", "app.jar"})

	assert.NoError(t, err)
	assert.Equal(t, []string{"/usr/bin/java", "-jar", "app.jar"}, argv)
}

func TestEntrypointArgvRefusesAnImageThatRunsNothing(t *testing.T) {
	_, err := entrypointArgv(nil, nil)

	assert.Error(t, err)
}

func TestEntrypointArgvUnwrapsAShellForm(t *testing.T) {
	argv, err := entrypointArgv(nil, []string{"/bin/sh", "-c", "node server.js"})

	assert.NoError(t, err)
	assert.Equal(t, []string{"node", "server.js"}, argv)
}

func TestEntrypointArgvDropsTheExecAShellFormAsksFor(t *testing.T) {
	argv, err := entrypointArgv([]string{"/bin/sh", "-c", "exec java -jar /app/app.jar"}, nil)

	assert.NoError(t, err)
	assert.Equal(t, []string{"java", "-jar", "/app/app.jar"}, argv)
}

func TestEntrypointArgvRefusesACommandThatNeedsAShell(t *testing.T) {
	needing := []string{
		"echo one && echo two",
		"exec java $JAVA_OPTS -jar /app.jar",
		"cat data | wc -l",
		"run > output",
	}

	for _, command := range needing {
		_, err := entrypointArgv(nil, []string{"/bin/sh", "-c", command})

		if assert.Error(t, err, command) {
			assert.Contains(t, err.Error(), "through a shell")
		}
	}
}

func TestEntrypointArgvSeesThroughAnInit(t *testing.T) {
	argv, err := entrypointArgv([]string{"/usr/bin/tini", "--", "/usr/local/bin/jenkins.sh"}, nil)

	assert.NoError(t, err)
	assert.Equal(t, []string{"/usr/local/bin/jenkins.sh"}, argv)
}

func TestEntrypointArgvSeesThroughAnInitCarryingFlags(t *testing.T) {
	argv, err := entrypointArgv([]string{"tini", "-s", "--", "/entrypoint.sh"}, []string{"start"})

	assert.NoError(t, err)
	assert.Equal(t, []string{"/entrypoint.sh", "start"}, argv)
}

// a file system to resolve paths in, laid out the way a container image lays one out
func sysrootForTest(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	assert.NoError(t, os.MkdirAll(filepath.Join(root, "usr", "bin"), 0755))
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "usr", "local", "bin"), 0755))

	elf := filepath.Join(root, "usr", "bin", "node")
	assert.NoError(t, os.WriteFile(elf, []byte("\x7fELF and then some"), 0755))

	script := filepath.Join(root, "usr", "local", "bin", "uvicorn")
	assert.NoError(t, os.WriteFile(script, []byte("#!/usr/local/bin/python\n"), 0755))

	// a merged /usr, which is what every debian and red hat base has
	assert.NoError(t, os.Symlink("usr/bin", filepath.Join(root, "bin")))
	// and a link that walks back out of its own directory
	assert.NoError(t, os.Symlink("../../bin/node", filepath.Join(root, "usr", "local", "bin", "node")))

	return root
}

func TestResolveInRootFollowsALinkedDirectory(t *testing.T) {
	root := sysrootForTest(t)

	resolved, err := resolveInRoot(root, "/bin/node")

	assert.NoError(t, err)
	assert.Equal(t, "/usr/bin/node", resolved)
}

func TestResolveInRootFollowsALinkThatWalksOut(t *testing.T) {
	root := sysrootForTest(t)

	resolved, err := resolveInRoot(root, "/usr/local/bin/node")

	assert.NoError(t, err)
	assert.Equal(t, "/usr/bin/node", resolved)
}

func TestResolveInRootRefusesWhatIsNotThere(t *testing.T) {
	root := sysrootForTest(t)

	_, err := resolveInRoot(root, "/usr/bin/absent")

	assert.Error(t, err)
}

func TestResolveProgramLooksAlongTheSearchPath(t *testing.T) {
	root := sysrootForTest(t)

	program, err := resolveProgram(root, "node", "", []string{"PATH=/usr/local/bin:/usr/bin"})

	assert.NoError(t, err)
	assert.Equal(t, "/usr/bin/node", program)
}

func TestResolveProgramTakesANameWithASeparatorFromTheWorkingDirectory(t *testing.T) {
	root := sysrootForTest(t)

	program, err := resolveProgram(root, "./bin/node", "/usr/local", nil)

	assert.NoError(t, err)
	assert.Equal(t, "/usr/bin/node", program)
}

func TestResolveProgramSaysWhenTheProgramIsAScript(t *testing.T) {
	root := sysrootForTest(t)

	_, err := resolveProgram(root, "/usr/local/bin/uvicorn", "", nil)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "runs a script")
	}
}

func TestResolveProgramSaysWhenTheProgramRewroteItsCommandLine(t *testing.T) {
	root := sysrootForTest(t)

	_, err := resolveProgram(root, "nginx:", "", nil)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "rewrites its own command line")
	}
}

func TestMainProgramTakesWhatTheLauncherStarted(t *testing.T) {
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/bin/sh /opt/jboss/wildfly/bin/standalone.sh -b 0.0.0.0"},
			{"42", "1", "/opt/java/openjdk/bin/java -Xmx512m org.jboss.as.standalone"},
		},
	}

	argv, pid := mainProgram(top)

	assert.Equal(t, []string{"/opt/java/openjdk/bin/java", "-Xmx512m", "org.jboss.as.standalone"}, argv)
	assert.Equal(t, "42", pid)
}

func TestMainProgramLeavesTheHelpersOfTheProgramAlone(t *testing.T) {
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/bin/sh /usr/local/bin/docker-entrypoint.sh rabbitmq-server"},
			{"20", "1", "/opt/erlang/bin/beam.smp -W w -- -root /opt/erlang"},
			{"31", "20", "/opt/erlang/lib/erlang/erts-15.2.7.13/bin/inet_gethost 4"},
		},
	}

	argv, _ := mainProgram(top)

	assert.Equal(t, "/opt/erlang/bin/beam.smp", argv[0])
}

func TestMainProgramPrefersTheOneStillBeingStarted(t *testing.T) {
	// an image that prepares itself runs the preparation beside the server, and it starts first
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/bin/sh /entrypoint.sh apache2-foreground"},
			{"12", "1", "/usr/bin/rsync -rlDog /usr/src/nextcloud/ /var/www/html/"},
			{"48", "1", "/usr/sbin/apache2 -DFOREGROUND"},
		},
	}

	argv, _ := mainProgram(top)

	assert.Equal(t, "/usr/sbin/apache2", argv[0])
}

func TestMainProgramSeesPastASupervisor(t *testing.T) {
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/package/admin/s6/command/s6-svscan -d4 -- /run/service"},
			{"14", "1", "/package/admin/s6-2.13.2.0/command/s6-notifyoncheck -d -n 300 -w 1000"},
			{"18", "1", "/bin/busybox sh /run/s6/basedir/scripts/rc.init"},
			{"22", "1", "/usr/bin/python3 /app/tautulli/Tautulli.py --datadir /config"},
		},
	}

	argv, _ := mainProgram(top)

	assert.Equal(t, "/usr/bin/python3", argv[0])
}

func TestMainProgramHasNothingToTakeFromAShell(t *testing.T) {
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/bin/sh /docker-entrypoint.sh"},
		},
	}

	argv, _ := mainProgram(top)

	assert.Nil(t, argv)
}

func TestStatusFieldReadsTheNumbersTheKernelKeeps(t *testing.T) {
	// what the kernel keeps about this very process, which is the only one whose numbers are
	// known here for certain
	pid := fmt.Sprint(os.Getpid())

	assert.Equal(t, fmt.Sprint(os.Getuid()), statusField(pid, "Uid:", 0))
	assert.Equal(t, fmt.Sprint(os.Geteuid()), statusField(pid, "Uid:", 1))
	assert.Equal(t, pid, statusField(pid, "NSpid:", -1))
	assert.Equal(t, "", statusField(pid, "NoSuchThing:", 0))
}

func TestExposedPortsSplitsTheProtocols(t *testing.T) {
	tcp, udp := exposedPorts(map[string]struct{}{
		"8080/tcp": {},
		"443":      {},
		"53/udp":   {},
	})

	assert.Equal(t, []string{"443", "8080"}, tcp)
	assert.Equal(t, []string{"53"}, udp)
}

func TestEnvironmentKeepsWhatTheImageSets(t *testing.T) {
	env := environment([]string{"PATH=/usr/bin", "GREETING=ciao", "MALFORMED"})

	assert.Equal(t, map[string]string{"PATH": "/usr/bin", "GREETING": "ciao"}, env)
}

func TestWriteHostsGivesLocalhostAnAddress(t *testing.T) {
	root := t.TempDir()

	assert.NoError(t, writeHosts(root))

	written, err := os.ReadFile(filepath.Join(root, "etc", "hosts"))
	assert.NoError(t, err)
	assert.Contains(t, string(written), "127.0.0.1")
	assert.Contains(t, string(written), "localhost")
}

func TestWriteHostsLeavesTheOneTheImageCarries(t *testing.T) {
	root := t.TempDir()
	carried := "10.0.0.1 localhost mine\n"

	assert.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(root, "etc", "hosts"), []byte(carried), 0644))
	assert.NoError(t, writeHosts(root))

	written, err := os.ReadFile(filepath.Join(root, "etc", "hosts"))
	assert.NoError(t, err)
	assert.Equal(t, carried, string(written))
}

func TestVolumeSizeLeavesRoomToWriteIn(t *testing.T) {
	root := t.TempDir()

	assert.Equal(t, "256m", volumeSize(root))
}

func TestIsKernelPathKnowsWhatTheKernelMakes(t *testing.T) {
	for _, kernel := range []string{"dev", "dev/null", "./proc/1/stat", "/sys/class"} {
		assert.True(t, isKernelPath(kernel), kernel)
	}

	for _, image := range []string{"etc/hosts", "usr/bin/java", "development/app", "systemd"} {
		assert.False(t, isKernelPath(image), image)
	}
}

func TestResolveBuildContextTakesTheDirectoryOfTheDockerfile(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0644))

	dockerfile, context, err := resolveBuildContext(filepath.Join(dir, "Dockerfile"), "")

	assert.NoError(t, err)
	assert.Equal(t, "Dockerfile", dockerfile)
	assert.Equal(t, dir, context)
}

func TestResolveBuildContextRefusesADockerfileOutsideIt(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch\n"), 0644))

	_, _, err := resolveBuildContext(filepath.Join(outside, "Dockerfile"), dir)

	assert.Error(t, err)
}
