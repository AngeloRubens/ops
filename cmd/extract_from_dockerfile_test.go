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

func TestMainProgramTakesTheServerALauncherStarts(t *testing.T) {
	// glassfish's asadmin is a jvm that starts the server's jvm and waits on it
	top := dockerContainer.TopResponse{
		Titles: []string{"PID", "PPID", "ARGS"},
		Processes: [][]string{
			{"1", "0", "/bin/bash /usr/local/bin/docker-entrypoint.sh startserv"},
			{"20", "1", "/opt/java/openjdk/bin/java -jar /opt/gfinstall/glassfish/lib/client/appserver-cli.jar start-domain"},
			{"45", "20", "/opt/java/openjdk/bin/java -cp glassfish.jar com.sun.enterprise.glassfish.bootstrap.ASMain"},
		},
	}

	argv, pid := mainProgram(top)

	assert.Equal(t, "45", pid)
	assert.Contains(t, argv, "com.sun.enterprise.glassfish.bootstrap.ASMain")
}

func TestListeningInReadsTheSocketsTheKernelKeeps(t *testing.T) {
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	listening := header + "   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345\n"
	connected := header + "   1: 0100007F:8C3E 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 12346\n"

	assert.True(t, listeningIn([]byte(listening)))
	assert.False(t, listeningIn([]byte(connected)))
	assert.False(t, listeningIn(nil))
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

func TestExactArgvKeepsEachArgumentWhole(t *testing.T) {
	// this very process, whose command line is known here for certain
	pid := fmt.Sprint(os.Getpid())

	assert.Equal(t, os.Args, exactArgv(pid, []string{os.Args[0]}))
	assert.Equal(t, []string{"nginx:", "master"}, exactArgv(pid, []string{"nginx:", "master"}))
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

func TestWriteMachineFilesCountsTheProcessors(t *testing.T) {
	root := t.TempDir()

	assert.NoError(t, writeMachineFiles(root, "amd64"))

	cpuinfo, err := os.ReadFile(filepath.Join(root, "proc", "cpuinfo"))
	assert.NoError(t, err)
	assert.Equal(t, "processor\t: 0\n\n", string(cpuinfo))

	possible, err := os.ReadFile(filepath.Join(root, "sys", "devices", "system", "cpu", "possible"))
	assert.NoError(t, err)
	assert.Equal(t, "0-0\n", string(possible))
}

func TestWriteMachineFilesLeavesTheKernelToAnswerOnArm(t *testing.T) {
	root := t.TempDir()

	assert.NoError(t, writeMachineFiles(root, "arm64"))

	_, err := os.Stat(filepath.Join(root, "proc", "cpuinfo"))
	assert.Error(t, err)
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

func TestWalkInRootNamesTheLinksOnTheWay(t *testing.T) {
	root := sysrootForTest(t)

	resolved, links, err := walkInRoot(root, "/bin/node")

	assert.NoError(t, err)
	assert.Equal(t, "/usr/bin/node", resolved)
	assert.Equal(t, []string{"/bin"}, links)
}

func writeForTest(t *testing.T, root string, p string, content string) {
	t.Helper()

	full := filepath.Join(root, p)
	assert.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	assert.NoError(t, os.WriteFile(full, []byte(content), 0644))
}

// a debian root file system the way an image carries one: a merged /usr, and a package database
// that names some of the files by where they were before the merge
func debianForTest(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "usr", "lib"), 0755))
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0755))
	assert.NoError(t, os.Symlink("usr/lib", filepath.Join(root, "lib")))

	writeForTest(t, root, "usr/lib/libc.so.6", "libc")
	writeForTest(t, root, "usr/bin/ldd", "#!/bin/bash\n")
	writeForTest(t, root, "usr/bin/perl", "perl")
	writeForTest(t, root, "usr/share/perl/strict.pm", "1;\n")
	writeForTest(t, root, "etc/perl/Config.pm", "1;\n")
	writeForTest(t, root, "etc/nsswitch.conf", "hosts: files dns\n")
	writeForTest(t, root, "opt/java/bin/java", "java")

	writeForTest(t, root, "var/lib/dpkg/status", `Package: libc6
Status: install ok installed
Architecture: amd64
Multi-Arch: same
Source: glibc
Description: the C library
 carried on a second line

Package: libc-bin
Status: install ok installed
Architecture: amd64
Source: glibc

Package: perl-base
Status: install ok installed
Architecture: amd64
Source: perl (5.36.0-7)

Package: gone
Status: deinstall ok config-files
Architecture: amd64
`)
	writeForTest(t, root, "var/lib/dpkg/info/libc6:amd64.list", "/.\n/lib\n/lib/libc.so.6\n")
	writeForTest(t, root, "var/lib/dpkg/info/libc-bin.list", "/.\n/usr/bin/ldd\n")
	writeForTest(t, root, "var/lib/dpkg/info/perl-base.list",
		"/.\n/usr/bin/perl\n/usr/share/perl/strict.pm\n/etc/perl/Config.pm\n")
	writeForTest(t, root, "var/lib/dpkg/info/gone.list", "/usr/bin/gone\n")

	return root
}

func TestPackageOwnersReadsWhatDpkgInstalled(t *testing.T) {
	owners := packageOwners(debianForTest(t))

	assert.Equal(t, "glibc", owners["/usr/lib/libc.so.6"])
	assert.Equal(t, "glibc", owners["/usr/bin/ldd"])
	assert.Equal(t, "perl", owners["/usr/bin/perl"])

	_, owned := owners["/usr/bin/gone"]
	assert.False(t, owned)
	_, owned = owners["/opt/java/bin/java"]
	assert.False(t, owned)
}

func TestPackageOwnersReadsWhatApkInstalled(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "lib/apk/db/installed",
		"C:Q1abc=\nP:musl\nV:1.2.5-r0\no:musl\nF:lib\nR:ld-musl-x86_64.so.1\n\n"+
			"P:busybox-binsh\nV:1.36.1-r29\no:busybox\nF:bin\nR:sh\nF:usr/bin\nR:env\n")

	owners := packageOwners(root)

	assert.Equal(t, "musl", owners["/lib/ld-musl-x86_64.so.1"])
	assert.Equal(t, "busybox", owners["/bin/sh"])
	assert.Equal(t, "busybox", owners["/usr/bin/env"])
}

func TestPackageOwnersReadsADistrolessDatabase(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "var/lib/dpkg/status.d/libssl3", "Package: libssl3\nSource: openssl\n")
	writeForTest(t, root, "var/lib/dpkg/status.d/libssl3.md5sums",
		"0123456789abcdef  usr/lib/x86_64-linux-gnu/libssl.so.3\n")

	owners := packageOwners(root)

	assert.Equal(t, "openssl", owners["/usr/lib/x86_64-linux-gnu/libssl.so.3"])
}

func existsForTest(root string, p string) bool {
	_, err := os.Lstat(filepath.Join(root, p))
	return err == nil
}

func TestLeaveOutTheOperatingSystemKeepsWhatThePackagesDidNotInstall(t *testing.T) {
	root := debianForTest(t)

	leaveOutTheOperatingSystem(root, "/opt/java/bin/java", []string{"/lib/libc.so.6"}, nil)

	assert.True(t, existsForTest(root, "opt/java/bin/java"), "what the image put there itself")
	assert.True(t, existsForTest(root, "usr/lib/libc.so.6"), "what the program reaches")
	assert.True(t, existsForTest(root, "etc/nsswitch.conf"), "what is read by name")
	assert.True(t, existsForTest(root, "tmp"), "a directory that was empty to begin with")
	assert.False(t, existsForTest(root, "usr/bin/ldd"), "what is packaged beside a library")
	assert.False(t, existsForTest(root, "usr/bin/perl"), "a package nothing reaches")
	assert.False(t, existsForTest(root, "etc/perl/Config.pm"), "its configuration")
	assert.False(t, existsForTest(root, "usr/share/perl"), "a directory that held only that package")
}

func TestLeaveOutTheOperatingSystemKeepsThePackageOfTheProgram(t *testing.T) {
	root := debianForTest(t)

	leaveOutTheOperatingSystem(root, "/usr/bin/perl", nil, nil)

	assert.True(t, existsForTest(root, "usr/bin/perl"))
	assert.True(t, existsForTest(root, "usr/share/perl/strict.pm"), "what the program reads by name")
	assert.True(t, existsForTest(root, "etc/perl/Config.pm"), "its configuration")
	assert.False(t, existsForTest(root, "usr/bin/ldd"), "a package the program does not come in")
}

func TestLeaveOutTheOperatingSystemKeepsEverythingWithoutADatabase(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "usr/bin/perl", "perl")

	leaveOutTheOperatingSystem(root, "/opt/app", nil, nil)

	_, err := os.Stat(filepath.Join(root, "usr", "bin", "perl"))
	assert.NoError(t, err)
}

func TestPackageOwnersCarriesAnImageWhoseRpmDatabaseCannotBeRead(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "var/lib/rpm/rpmdb.sqlite", "not a database at all")

	assert.Empty(t, packageOwners(root))
}

func TestRpmSourceTakesTheVersionOff(t *testing.T) {
	assert.Equal(t, "glibc", rpmSource("glibc-2.34-100.el9.src.rpm", "glibc-common"))
	assert.Equal(t, "java-21-openjdk", rpmSource("java-21-openjdk-21.0.4.0.7-2.el9.src.rpm", "java-21-openjdk-headless"))
	assert.Equal(t, "gpg-pubkey", rpmSource("(none)", "gpg-pubkey"))
}

func TestLibraryDirsReadsTheLinkerConfiguration(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "etc/ld.so.conf", "include /etc/ld.so.conf.d/*.conf\n")
	writeForTest(t, root, "etc/ld.so.conf.d/x86_64-linux-gnu.conf",
		"# Multiarch support\n/usr/local/lib/x86_64-linux-gnu\n/lib/x86_64-linux-gnu\n")

	dirs := libraryDirs(root)

	assert.Equal(t, []string{"/usr/local/lib/x86_64-linux-gnu", "/lib/x86_64-linux-gnu"}, dirs[:2])
	assert.Contains(t, dirs, "/usr/lib")
}

func TestFindLibraryLooksWhereTheLinkerLooks(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "usr/lib/libz.so.1", "system")
	writeForTest(t, root, "opt/app/lib/libz.so.1", "bundled")
	writeForTest(t, root, "srv/lib/libz.so.1", "asked for")

	r := newReach(root, nil)
	assert.Equal(t, "/usr/lib/libz.so.1", r.findLibrary("libz.so.1", "/opt/app/bin", nil, nil))
	assert.Equal(t, "/opt/app/lib/libz.so.1",
		r.findLibrary("libz.so.1", "/opt/app/bin", nil, []string{"$ORIGIN/../lib"}))
	assert.Equal(t, "", r.findLibrary("libabsent.so", "/opt/app/bin", nil, nil))

	r = newReach(root, []string{"LD_LIBRARY_PATH=/srv/lib"})
	assert.Equal(t, "/srv/lib/libz.so.1", r.findLibrary("libz.so.1", "/opt/app/bin", nil, nil))
}

func TestNamedInFindsTheLibrariesAProgramOpensItself(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "native.so",
		"\x7fELF\x00libssl.so.3\x00libicuuc.so.%d\x00/usr/lib/libz.so\x00libssl.so.3\x00plain words\x00")

	assert.Equal(t, []string{"libssl.so.3", "libicuuc.so", "libz.so"},
		namedIn(filepath.Join(root, "native.so")))
}

func TestOpenedByNameTakesEveryVersionOfAnUnversionedName(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "usr/lib/libicuuc.so.72", "icu")
	writeForTest(t, root, "usr/lib/libicuuc.so.72.1", "icu")
	writeForTest(t, root, "usr/lib/libssl.so.3", "ssl")

	r := newReach(root, nil)

	assert.Equal(t, []string{"/usr/lib/libssl.so.3"}, r.openedByName("libssl.so.3"))
	assert.Equal(t, []string{"/usr/lib/libicuuc.so.72", "/usr/lib/libicuuc.so.72.1"},
		r.openedByName("libicuuc.so"))
	assert.Nil(t, r.openedByName("libssl.so.1.1"))
}

func TestNssServicesReadsWhatNsswitchNames(t *testing.T) {
	root := t.TempDir()
	writeForTest(t, root, "etc/authselect/nsswitch.conf",
		"# hosts: commented\npasswd: files sss systemd\nhosts: files myhostname resolve [!UNAVAIL=return] dns\n")
	assert.NoError(t, os.Symlink("authselect/nsswitch.conf", filepath.Join(root, "etc", "nsswitch.conf")))

	assert.Equal(t, []string{"files", "sss", "systemd", "myhostname", "resolve", "dns"}, nssServices(root))
}

func TestMappedPathsReadsTheFilesOfAMap(t *testing.T) {
	maps := "55d0c0a00000-55d0c0a02000 r--p 00000000 00:2a 1234 /usr/local/bin/redis-server\n" +
		"7f1c2a000000-7f1c2a021000 rw-p 00000000 00:00 0 \n" +
		"7f1c2a400000-7f1c2a428000 r--p 00000000 00:2a 99 /usr/lib/x86_64-linux-gnu/libc.so.6\n" +
		"7f1c2a600000-7f1c2a601000 rw-s 00000000 00:01 7 /memfd:jit (deleted)\n" +
		"7f1c2a800000-7f1c2a801000 r--p 00000000 00:2a 12 /opt/app/a name with spaces.so\n" +
		"7ffd1e5f0000-7ffd1e611000 rw-p 00000000 00:00 0 [stack]\n"

	assert.Equal(t, []string{
		"/usr/local/bin/redis-server",
		"/usr/lib/x86_64-linux-gnu/libc.so.6",
		"/memfd:jit",
		"/opt/app/a name with spaces.so",
	}, mappedPaths([]byte(maps)))
}
