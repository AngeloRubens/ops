package cmd

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	dockerBuild "github.com/docker/docker/api/types/build"
	dockerContainer "github.com/docker/docker/api/types/container"
	dockerImage "github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/term"

	api "github.com/nanovms/ops/lepton"
	"github.com/nanovms/ops/types"
)

// pathWhenUnset is the search path a docker image is given when its configuration does not
// carry one of its own.
const pathWhenUnset = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// initWrappers are the small supervisors an image puts in front of its program to reap children
// and forward signals. A unikernel has no children to reap and no second process to forward to,
// so what counts is the program behind them.
var initWrappers = []string{"tini", "dumb-init", "catatonit"}

// supervisors are the programs that start other programs and stay to watch them. What the image
// was built to run is the thing they start, not them or the errands they run.
var supervisors = []string{
	"init", "runsv", "runsvdir", "supervisord", "tini", "dumb-init", "catatonit",
}

// isSupervisor reports whether a program is one that starts other programs rather than one to be
// started. s6, which every linuxserver image is built on, brings dozens of little tools and they
// are all named alike.
func isSupervisor(name string) bool {
	return strings.HasPrefix(name, "s6-") || contains(supervisors, name)
}

// DockerfileOptions describes a package to build out of a Dockerfile.
type DockerfileOptions struct {
	Dockerfile  string
	Context     string
	PackageName string
	Version     string
	Arch        string
	Target      string
	BuildArgs   map[string]*string
	KeepImage   bool
	Quiet       bool
	Verbose     bool

	// Resolve asks what the image runs by running it and watching what it starts, rather than
	// by reading its configuration.
	Resolve        bool
	ResolveTimeout time.Duration
}

// BuildFromDockerfile builds a Dockerfile into a docker image and turns that image into a
// package: the file system of the image becomes the package sysroot and the configuration the
// image carries - its entrypoint, environment, working directory and exposed ports - becomes
// the package manifest.
func BuildFromDockerfile(opts DockerfileOptions) (string, string, error) {
	dockerfile, contextDir, err := resolveBuildContext(opts.Dockerfile, opts.Context)
	if err != nil {
		return "", "", err
	}

	if opts.Version == "" {
		opts.Version = "latest"
	}
	if opts.PackageName == "" {
		opts.PackageName = filepath.Base(contextDir) + "_" + opts.Version
	}

	ctx := context.Background()
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		return "", "", err
	}
	defer cli.Close()

	tag := fmt.Sprintf("ops-from-dockerfile:%d", time.Now().UnixNano())
	if err := buildDockerImage(ctx, cli, tag, dockerfile, contextDir, opts); err != nil {
		return "", "", err
	}
	if !opts.KeepImage {
		defer cli.ImageRemove(ctx, tag, dockerImage.RemoveOptions{Force: true, PruneChildren: true})
	}

	inspected, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		return "", "", err
	}
	if inspected.Config == nil {
		return "", "", fmt.Errorf("the image built from %s carries no configuration", dockerfile)
	}
	config := inspected.Config

	argv, argvErr := entrypointArgv(config.Entrypoint, config.Cmd)
	ran, runsAs, runsAsGroup := "", "", ""
	if opts.Resolve {
		found, err := resolveByRunning(ctx, cli, tag, opts.ResolveTimeout, opts.Verbose)
		if found != nil {
			ran = found.id
		}
		if ran != "" {
			defer cli.ContainerRemove(ctx, ran, dockerContainer.RemoveOptions{Force: true})
		}
		if err != nil {
			// The image was asked and did not answer; what it declares is all there is.
			fmt.Printf("warning: could not see what the image starts: %v\n", err)
		} else {
			fmt.Printf("the image starts %s\n", strings.Join(found.argv, " "))
			argv, argvErr = found.argv, nil

			// what the launcher exported on its way, which the image does not declare
			if len(found.env) == 0 {
				fmt.Println("warning: could not read the environment the launcher exported, so " +
					"only what the image declares is carried")
			}
			config.Env = append(config.Env, found.env...)
			runsAs, runsAsGroup = found.uid, found.gid
		}
	}
	if argvErr != nil {
		return "", "", argvErr
	}

	tempDirectory, err := os.MkdirTemp("", "*")
	if err != nil {
		return "", "", err
	}
	sysroot := filepath.Join(tempDirectory, "sysroot")
	if opts.Verbose {
		fmt.Printf("Extracting the image file system into %s\n", sysroot)
	}
	if err := exportImageFS(ctx, cli, tag, ran, sysroot); err != nil {
		return "", "", err
	}

	if err := writeHosts(sysroot); err != nil {
		return "", "", err
	}

	discardWhatCannotBeReached(sysroot)

	program, err := resolveProgram(sysroot, argv[0], config.WorkingDir, config.Env)
	if err != nil {
		return "", "", err
	}

	c := &types.Config{
		Program:         program,
		Args:            argv,
		Env:             environment(config.Env),
		Version:         opts.Version,
		DisableArgsCopy: true,
	}
	c.BaseVolumeSz = volumeSize(sysroot)
	c.RunConfig.Ports, c.RunConfig.UDPPorts = exposedPorts(config.ExposedPorts)
	c.ManifestPassthrough = map[string]any{}
	if wd := config.WorkingDir; wd != "" && wd != "/" {
		c.ManifestPassthrough["cwd"] = wd
		c.Env["PWD"] = wd
	}

	// who the program ran as, for the programs that will not be root
	if runsAs != "" && runsAs != "0" {
		c.ManifestPassthrough["uid"] = runsAs
		c.ManifestPassthrough["gid"] = runsAsGroup
	}
	if len(c.ManifestPassthrough) == 0 {
		c.ManifestPassthrough = nil
	}

	reportIgnored(config.User, config.Healthcheck, config.Volumes)
	reportContents(sysroot)

	manifest, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(tempDirectory, "package.manifest"), manifest, 0666); err != nil {
		return "", "", err
	}

	packageDirectory := MovePackageFiles(tempDirectory,
		filepath.Join(api.LocalPackagesRoot, opts.Arch, opts.PackageName))

	return opts.PackageName, packageDirectory, nil
}

// resolveBuildContext returns the name of the Dockerfile relative to the build context, which
// is what the daemon is given, along with the context itself.
func resolveBuildContext(dockerfile string, contextDir string) (string, string, error) {
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	dockerfile, err := filepath.Abs(dockerfile)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(dockerfile)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%s is a directory, not a Dockerfile", dockerfile)
	}

	if contextDir == "" {
		contextDir = filepath.Dir(dockerfile)
	}
	contextDir, err = filepath.Abs(contextDir)
	if err != nil {
		return "", "", err
	}

	relative, err := filepath.Rel(contextDir, dockerfile)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(relative, "..") {
		return "", "", fmt.Errorf("the Dockerfile %s is outside the build context %s", dockerfile, contextDir)
	}

	return filepath.ToSlash(relative), contextDir, nil
}

// buildDockerImage builds the Dockerfile. The daemon API on its own reaches the builder docker
// has carried since the beginning, which knows nothing of COPY --chmod, RUN --mount, heredocs or
// $BUILDPLATFORM - the syntax most Dockerfiles are written in today. The docker command, when
// there is one, reaches buildkit, so it is preferred and the API is what is left when it is
// missing.
func buildDockerImage(ctx context.Context, cli *dockerClient.Client, tag string, dockerfile string,
	contextDir string, opts DockerfileOptions) error {
	if docker, err := exec.LookPath("docker"); err == nil {
		return buildWithDockerCommand(ctx, docker, tag, dockerfile, contextDir, opts)
	}
	fmt.Println("warning: no docker command to build with, so the build is left to the daemon's " +
		"own builder, which does not understand the newer dockerfile syntax")

	return buildWithDaemon(ctx, cli, tag, dockerfile, contextDir, opts)
}

// buildWithDockerCommand runs the build through the docker command, and so through buildkit.
func buildWithDockerCommand(ctx context.Context, docker string, tag string, dockerfile string,
	contextDir string, opts DockerfileOptions) error {
	args := []string{"build", "--file", filepath.Join(contextDir, dockerfile), "--tag", tag}
	for name, value := range opts.BuildArgs {
		if value != nil {
			args = append(args, "--build-arg", name+"="+*value)
		}
	}
	if opts.Target != "" {
		args = append(args, "--target", opts.Target)
	}
	if opts.Arch != "" && opts.Arch != runtime.GOARCH {
		args = append(args, "--platform", "linux/"+opts.Arch)
	}
	if opts.Quiet {
		args = append(args, "--quiet")
	}
	args = append(args, contextDir)

	build := exec.CommandContext(ctx, docker, args...)
	build.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr

	return build.Run()
}

func buildWithDaemon(ctx context.Context, cli *dockerClient.Client, tag string, dockerfile string,
	contextDir string, opts DockerfileOptions) error {
	tarball, err := os.CreateTemp("", "ops-build-context-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(tarball.Name())
	defer tarball.Close()

	if err := tarDirectory(contextDir, tarball); err != nil {
		return err
	}
	if _, err := tarball.Seek(0, io.SeekStart); err != nil {
		return err
	}

	buildOptions := dockerBuild.ImageBuildOptions{
		Dockerfile:  dockerfile,
		Tags:        []string{tag},
		BuildArgs:   opts.BuildArgs,
		Target:      opts.Target,
		Remove:      true,
		ForceRemove: true,
	}
	// Only asked for when it differs from the host, as the builder the daemon uses for these
	// builds refuses a platform of its own accord.
	if opts.Arch != "" && opts.Arch != runtime.GOARCH {
		buildOptions.Platform = "linux/" + opts.Arch
	}

	response, err := cli.ImageBuild(ctx, tarball, buildOptions)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	out := io.Writer(os.Stdout)
	if opts.Quiet {
		out = io.Discard
	}
	termFd, isTerm := term.GetFdInfo(os.Stderr)

	return jsonmessage.DisplayJSONMessagesStream(response.Body, out, termFd, isTerm, nil)
}

// tarDirectory writes dir as the tar stream the daemon expects as a build context.
func tarDirectory(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	return filepath.Walk(dir, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if info.IsDir() && name == ".git" {
			return filepath.SkipDir
		}

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(file); err != nil {
				return err
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			// sockets and devices have no place in a build context, and tar cannot carry them
			return nil
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// exportImageFS lays a file system into sysroot. When the image has been run to see what it
// starts, the file system taken is the one that running it left behind, which is how an image
// that sets itself up on first boot - initialising a database, writing out a configuration -
// has that work in the package rather than ahead of it. Otherwise a container is made for the
// files and never started.
func exportImageFS(ctx context.Context, cli *dockerClient.Client, tag string, ran string,
	sysroot string) error {
	if ran != "" {
		return copyWholeContainer(cli, ran, sysroot)
	}

	created, err := cli.ContainerCreate(ctx, &dockerContainer.Config{Image: tag}, nil, nil, nil, "")
	if err != nil {
		return err
	}
	defer cli.ContainerRemove(ctx, created.ID, dockerContainer.RemoveOptions{Force: true})

	return copyWholeContainer(cli, created.ID, sysroot)
}

// resolveByRunning starts the image and reads what it starts off the process tree. A launcher
// script ends in an exec of the program it was written to start, so once the tree has settled
// the program is there to be read, with the arguments the script worked out - which is what
// makes an image built around a launcher describable by a manifest at all.
// started is what an image turned out to be doing when it was asked, and as whom.
type started struct {
	argv []string
	env  []string
	uid  string
	gid  string
	id   string
}

func resolveByRunning(ctx context.Context, cli *dockerClient.Client, tag string,
	timeout time.Duration, verbose bool) (*started, error) {
	if timeout <= 0 {
		timeout = time.Minute
	}

	created, err := cli.ContainerCreate(ctx, &dockerContainer.Config{Image: tag}, nil, nil, nil, "")
	if err != nil {
		return nil, err
	}

	found := &started{id: created.ID}

	if err := cli.ContainerStart(ctx, created.ID, dockerContainer.StartOptions{}); err != nil {
		return found, err
	}
	defer cli.ContainerStop(ctx, created.ID, dockerContainer.StopOptions{})

	deadline := time.Now().Add(timeout)
	var settled []string
	times := 0

	for time.Now().Before(deadline) {
		time.Sleep(time.Second)

		top, err := cli.ContainerTop(ctx, created.ID, []string{"-eo", "pid,ppid,args"})
		if err != nil {
			// the container is gone, and with it anything left to read
			break
		}

		argv, pid := mainProgram(top)
		if argv == nil {
			continue
		}
		if verbose {
			fmt.Printf("running: %s\n", strings.Join(argv, " "))
		}

		if sameArgv(argv, settled) {
			// the same program over several turns: what the image sets up first is gone by then
			if times++; times >= 4 {
				found.argv = argv
				found.env = environOf(ctx, cli, created.ID, pid, verbose)
				found.uid, found.gid = whoIsRunning(pid)
				return found, nil
			}
			continue
		}

		settled, times = argv, 1
		found.argv = argv
		found.env = environOf(ctx, cli, created.ID, pid, verbose)
		found.uid, found.gid = whoIsRunning(pid)
	}

	if found.argv != nil {
		return found, nil
	}

	return found, fmt.Errorf("nothing but the launcher was running within %s", timeout)
}

// mainProgram picks what the launcher was written to start: the process nearest the top of the
// tree that is neither a shell nor a supervisor. Nearest rather than furthest, because the
// programs below it are its own - an erlang runtime keeps a name resolver, grafana installs its
// plugins - and the latest of those at the same remove, because a setup step run beside it
// starts first and is gone by the time the server is up.
func mainProgram(top dockerContainer.TopResponse) ([]string, string) {
	pidAt, ppidAt, argsAt := -1, -1, -1
	for i, title := range top.Titles {
		switch strings.ToUpper(title) {
		case "PID":
			pidAt = i
		case "PPID":
			ppidAt = i
		case "ARGS", "COMMAND", "CMD":
			argsAt = i
		}
	}
	if pidAt < 0 || ppidAt < 0 || argsAt < 0 {
		return nil, ""
	}

	type process struct {
		ppid string
		argv []string
	}
	processes := map[string]process{}
	pids := []string{}

	for _, row := range top.Processes {
		if len(row) <= argsAt {
			continue
		}
		argv := strings.Fields(strings.Join(row[argsAt:], " "))
		if len(argv) == 0 {
			continue
		}
		processes[row[pidAt]] = process{ppid: row[ppidAt], argv: argv}
		pids = append(pids, row[pidAt])
	}
	sort.Strings(pids)

	depthOf := func(pid string) int {
		depth := 0
		for hops := 0; hops < 64; hops++ {
			p, ok := processes[pid]
			if !ok {
				break
			}
			if _, ok := processes[p.ppid]; !ok {
				break
			}
			pid = p.ppid
			depth++
		}
		return depth
	}

	var best []string
	bestPid := ""
	nearest, latest := -1, -1

	for _, pid := range pids {
		name := filepath.Base(processes[pid].argv[0])
		// busybox is a shell as often as not, and under s6 it always is
		if name == "sh" || name == "bash" || name == "busybox" || name == "ps" || isSupervisor(name) {
			continue
		}

		depth := depthOf(pid)
		started, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		if nearest >= 0 && (depth > nearest || (depth == nearest && started < latest)) {
			continue
		}

		best, bestPid, nearest, latest = processes[pid].argv, pid, depth, started
	}

	return best, bestPid
}

// environOf reads the environment a process was given, which is the half of what a launcher does
// that its command line does not show: rabbitmq will not start without the BINDIR its own script
// exports. The pid docker reports is the one the host knows the process by, and a process
// belonging to another user keeps its environment to itself, so when the file cannot be read
// here it is read from inside the container, where root is root.
func environOf(ctx context.Context, cli *dockerClient.Client, container string, pid string,
	verbose bool) []string {
	if raw, err := os.ReadFile(filepath.Join("/proc", pid, "environ")); err == nil {
		return environEntries(raw)
	} else if verbose {
		fmt.Printf("reading the environment of %s: %v\n", pid, err)
	}

	// The number the container knows it by, when the host will say what that is.
	if inside := containerPid(pid); inside != "" {
		raw, err := readInContainer(ctx, cli, container, "/proc/"+inside+"/environ")
		if err == nil {
			return environEntries(raw)
		}
		if verbose {
			fmt.Printf("reading the launcher's environment inside the container: %v\n", err)
		}
	} else if verbose {
		fmt.Printf("the container's own number for %s is not to be had\n", pid)
	}

	// A launcher that hands over with an exec leaves the program as the first process of the
	// container, and that one is always there to be read.
	raw, err := readInContainer(ctx, cli, container, "/proc/1/environ")
	if err != nil {
		if verbose {
			fmt.Printf("reading the first process's environment inside the container: %v\n", err)
		}
		return nil
	}

	return environEntries(raw)
}

func environEntries(raw []byte) []string {
	var env []string

	for _, entry := range strings.Split(string(raw), "\x00") {
		if strings.Contains(entry, "=") {
			env = append(env, entry)
		}
	}

	return env
}

// containerPid is the number the process goes by inside its own container, which is what a path
// under /proc means to anything running in there. The kernel lists both, outermost first.
func containerPid(pid string) string {
	return statusField(pid, "NSpid:", -1)
}

// whoIsRunning is the user and group the program turned out to run as. An image is free to say
// nothing about it and change hands on its way up - the postgres image is root and its entrypoint
// hands over to the postgres user through gosu - so the only honest place to look is the process
// that ended up running. It matters because a program may decline to be root: postgres stops at
// "root" execution of the PostgreSQL server is not permitted, and no flag says otherwise.
func whoIsRunning(pid string) (string, string) {
	return statusField(pid, "Uid:", 1), statusField(pid, "Gid:", 1)
}

// statusField reads one of the numbers the kernel keeps about a process. Several of them are
// lists - the uid line carries the real one, the effective one and two more, and the pid line
// carries one number per namespace the process is in - so which is wanted is said by index,
// counting from the end when it is negative.
func statusField(pid string, name string, at int) string {
	status, err := os.ReadFile(filepath.Join("/proc", pid, "status"))
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}

		fields := strings.Fields(strings.TrimPrefix(line, name))
		if at < 0 {
			at += len(fields)
		}
		if at < 0 || at >= len(fields) {
			return ""
		}

		return fields[at]
	}

	return ""
}

// readInContainer reads a file from inside a running container, as root, since that is who can
// read another user's environment.
func readInContainer(ctx context.Context, cli *dockerClient.Client, container string,
	path string) ([]byte, error) {
	created, err := cli.ContainerExecCreate(ctx, container, dockerContainer.ExecOptions{
		User:         "root",
		Cmd:          []string{"cat", path},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}

	attached, err := cli.ContainerExecAttach(ctx, created.ID, dockerContainer.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	defer attached.Close()

	out, said := &bytes.Buffer{}, &bytes.Buffer{}
	if _, err := stdcopy.StdCopy(out, said, attached.Reader); err != nil {
		return nil, err
	}

	if out.Len() == 0 {
		// an exec that runs and brings nothing back has something to say about why
		status, err := cli.ContainerExecInspect(ctx, created.ID)
		if err == nil {
			return nil, fmt.Errorf("%s came back empty, exit %d: %s", path, status.ExitCode,
				strings.TrimSpace(said.String()))
		}
		return nil, fmt.Errorf("%s came back empty: %s", path, strings.TrimSpace(said.String()))
	}

	return out.Bytes(), nil
}

func sameArgv(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// discardable are the trees nothing in a unikernel can reach for: documentation, manual pages,
// message catalogues, and what a package manager keeps in order to know what it installed. No
// package manager can run here - nanos starts one program and has no exec to start a second -
// so what they know is of no use to anyone.
var discardable = []string{
	"usr/share/man", "usr/share/info", "usr/share/doc/.build-id", "usr/share/gtk-doc",
	"usr/share/locale", "var/cache", "var/lib/apt/lists", "var/lib/dpkg/info", "var/lib/rpm",
}

func discardWhatCannotBeReached(sysroot string) {
	for _, tree := range discardable {
		os.RemoveAll(filepath.Join(sysroot, tree))
	}
}

// reportContents says what the package is made of. A unikernel is meant to carry what its
// program needs, and an image carries what a distribution installs, so the difference between
// the two is worth seeing rather than guessing at.
func reportContents(sysroot string) {
	trees := map[string]int64{}
	var total int64

	filepath.Walk(sysroot, func(p string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()

		rest, err := filepath.Rel(sysroot, p)
		if err != nil {
			return nil
		}
		top := strings.SplitN(rest, string(os.PathSeparator), 2)[0]
		trees[top] += info.Size()

		return nil
	})

	names := make([]string, 0, len(trees))
	for name := range trees {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return trees[names[i]] > trees[names[j]] })

	fmt.Printf("the file system is %d MB:", total/(1024*1024))
	for i, name := range names {
		if i == 4 {
			break
		}
		fmt.Printf(" /%s %dMB", name, trees[name]/(1024*1024))
	}
	fmt.Println()
}

// writeHosts writes the hosts file a container runtime writes for a container and an image does
// not carry. Without it localhost resolves to nothing - etcd cannot open its peer listener and
// caddy cannot open its administration endpoint, and both stop where they are.
func writeHosts(sysroot string) error {
	hosts := filepath.Join(sysroot, "etc", "hosts")

	if carried, err := os.ReadFile(hosts); err == nil && strings.Contains(string(carried), "localhost") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(hosts), 0755); err != nil {
		return err
	}

	return os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost ip6-loopback\n"), 0644)
}

// entrypointArgv works out what the image would run, the way docker does: the entrypoint with
// the command appended to it, with whatever the image puts in front of its program peeled off.
func entrypointArgv(entrypoint []string, cmd []string) ([]string, error) {
	argv := append(append([]string{}, entrypoint...), cmd...)
	if len(argv) == 0 {
		return nil, fmt.Errorf("the image declares neither an ENTRYPOINT nor a CMD, so there is nothing to run")
	}

	for layers := 0; layers < 4; layers++ {
		name := filepath.Base(argv[0])

		switch {
		// A shell form ENTRYPOINT or CMD is wrapped in a shell, which a unikernel does not
		// have. Unwrap it when it holds a plain command, and refuse it when it needs a shell.
		case (name == "sh" || name == "bash") && len(argv) >= 3 && argv[1] == "-c":
			words, err := plainCommand(argv[2])
			if err != nil {
				return nil, err
			}
			argv = append(words, argv[3:]...)

		case contains(initWrappers, name):
			behind := skipFlags(argv[1:])
			if len(behind) == 0 {
				return nil, fmt.Errorf("%s is the whole command, and it starts nothing", argv[0])
			}
			argv = behind

		default:
			return argv, nil
		}
	}

	return argv, nil
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// skipFlags drops the options of a wrapper and the -- that ends them, leaving the command.
func skipFlags(args []string) []string {
	for len(args) > 0 {
		if args[0] == "--" {
			return args[1:]
		}
		if !strings.HasPrefix(args[0], "-") {
			return args
		}
		args = args[1:]
	}
	return nil
}

// plainCommand splits a shell command into its words, refusing one that a shell would have to
// interpret: nanos starts a single program, with no shell to expand, redirect or chain.
func plainCommand(command string) ([]string, error) {
	if strings.ContainsAny(command, "|&;<>()$`\\\"'*?\n") {
		return nil, fmt.Errorf("the image runs %q through a shell, which nanos does not have; "+
			"give the Dockerfile an exec form CMD or ENTRYPOINT naming the program to run", command)
	}
	words := strings.Fields(command)
	// 'exec java -jar app.jar' is how an image says it wants no shell left behind; it is the
	// command that matters.
	if len(words) > 1 && words[0] == "exec" {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("the image declares an empty command")
	}
	return words, nil
}

// resolveProgram finds the program the image would run inside sysroot, the way an exec would:
// a name holding a separator is taken relative to the working directory and one without is
// looked up along the search path. It is a program only if it is an ELF.
func resolveProgram(sysroot string, name string, workingDir string, env []string) (string, error) {
	var candidates []string
	if strings.Contains(name, "/") {
		if filepath.IsAbs(name) {
			candidates = []string{filepath.Clean(name)}
		} else {
			if workingDir == "" {
				workingDir = "/"
			}
			candidates = []string{filepath.Join(workingDir, name)}
		}
	} else {
		for _, dir := range strings.Split(searchPath(env), ":") {
			if dir == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}

	for _, candidate := range candidates {
		resolved, err := resolveInRoot(sysroot, candidate)
		if err != nil {
			continue
		}
		if err := isELF(filepath.Join(sysroot, resolved)); err != nil {
			return "", err
		}
		return resolved, nil
	}

	// A program is free to rewrite its own command line, and several of the ones people run do:
	// ps shows nginx as "nginx: master process ...", which names nothing that can be started.
	if strings.HasSuffix(name, ":") {
		return "", fmt.Errorf("%s rewrites its own command line, so what it starts cannot be read "+
			"from the process tree; set Program and Args in a configuration file instead",
			strings.TrimSuffix(name, ":"))
	}

	return "", fmt.Errorf("%s, which the image runs, is not in the image", name)
}

func searchPath(env []string) string {
	for _, e := range env {
		if name, value, found := strings.Cut(e, "="); found && name == "PATH" {
			return value
		}
	}
	return pathWhenUnset
}

// resolveInRoot resolves p as it would resolve inside root, following the symbolic links it
// meets on the way and keeping every one of them inside root.
func resolveInRoot(root string, p string) (string, error) {
	resolved := "/"
	components := strings.Split(filepath.Clean(p), "/")

	for hops := 0; len(components) > 0; {
		component := components[0]
		components = components[1:]
		if component == "" || component == "." {
			continue
		}

		next := filepath.Join(resolved, component)
		info, err := os.Lstat(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}

		hops++
		if hops > 32 {
			return "", fmt.Errorf("too many levels of symbolic links resolving %s", p)
		}
		target, err := os.Readlink(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = "/"
		}
		components = append(strings.Split(filepath.Clean(target), "/"), components...)
	}

	return resolved, nil
}

// isELF reports what the file is when it is not a program nanos can start.
func isELF(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("%s is too short to be a program", p)
	}
	if string(header) == "\x7fELF" {
		return nil
	}
	if string(header[:2]) == "#!" {
		return fmt.Errorf("the image runs a script, and nanos starts a single ELF with no shell " +
			"or interpreter to run one: point the Dockerfile at the program the script ends up " +
			"running, or set Program and Args in a configuration file")
	}
	return fmt.Errorf("what the image runs is not an ELF executable")
}

// volumeSize is what to make the file system, which is what the image carries and then room to
// work in: a server writes - a lock file, a database, its own logs - and an image sized to fit
// exactly what it was given has nowhere to put any of it. Wildfly stops at "No space left on
// device" trying to write the uuid it makes on first boot.
func volumeSize(sysroot string) string {
	var total int64

	filepath.Walk(sysroot, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})

	megabytes := total / (1024 * 1024)

	return fmt.Sprintf("%dm", megabytes+megabytes/4+256)
}

func environment(env []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		if name, value, found := strings.Cut(e, "="); found {
			out[name] = value
		}
	}
	return out
}

// exposedPorts splits an EXPOSE set into the tcp and udp ports ops forwards.
func exposedPorts(exposed map[string]struct{}) ([]string, []string) {
	var tcp, udp []string
	for port := range exposed {
		number, protocol, found := strings.Cut(port, "/")
		if found && protocol == "udp" {
			udp = append(udp, number)
		} else {
			tcp = append(tcp, number)
		}
	}
	sort.Strings(tcp)
	sort.Strings(udp)
	return tcp, udp
}

// reportIgnored says which parts of the image configuration have no meaning in a unikernel,
// rather than leaving them to be missed at boot.
func reportIgnored(user string, healthcheck *dockerspec.HealthcheckConfig, volumes map[string]struct{}) {
	if user != "" && user != "root" && user != "0" && user != "0:0" {
		fmt.Printf("warning: the image runs as %s; a unikernel is a single process and runs as root\n", user)
	}
	if healthcheck != nil && len(healthcheck.Test) > 0 {
		fmt.Println("warning: HEALTHCHECK is ignored: there is no second process to run it")
	}
	if len(volumes) > 0 {
		names := make([]string, 0, len(volumes))
		for name := range volumes {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("warning: VOLUME %s is ignored: attach a volume with --mounts instead\n",
			strings.Join(names, ", "))
	}
}
