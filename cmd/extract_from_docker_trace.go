package cmd

import (
	"archive/tar"
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	dockerContainer "github.com/docker/docker/api/types/container"
	dockerImage "github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"
	api "github.com/nanovms/ops/lepton"
	"github.com/nanovms/ops/types"
)

// The rule: the package carries the files the program reaches.
//
// A program reaches a file in two ways. It asks for it, with a call that names it: open, stat,
// readlink, execve. An image run under strace gives that list, from the first instruction on: the
// libraries the program is linked against and the ones it loads by name, the configuration it
// reads, the data it opens. And the kernel asks for a file on its behalf when it executes a
// program: the interpreter that program names, which is the dynamic loader of an ELF file or the
// program after #! in a script. No call of the program names that file, so it is added for every
// file the program executed, and for the interpreter of that interpreter, and so on.
//
// Nothing else of the image is carried.

// traceMount is where the directory holding strace, and the list it writes, is seen from inside
// the container.
const traceMount = "/.ops-trace"

// stracePackager is the image strace is taken from. Alpine's is built against musl alone, which
// makes it a program that can be carried into an image of any other distribution along with the
// musl loader.
const stracePackager = "alpine:3.20"

// tracer is a directory on this side that holds strace and the loader to run it with, and gets the
// list of files the traced program opened.
type tracer struct {
	dir    string
	loader string
}

// prepareTracer fetches strace and what it is linked against into a directory of its own.
func prepareTracer(ctx context.Context, cli *dockerClient.Client) (*tracer, error) {
	dir, err := os.MkdirTemp("", "ops-trace-*")
	if err != nil {
		return nil, err
	}
	// whoever the traced program runs as writes the list and reads strace
	if err := os.Chmod(dir, 0777); err != nil {
		return nil, err
	}

	pulled, err := cli.ImagePull(ctx, stracePackager, dockerImage.PullOptions{})
	if err != nil {
		return nil, fmt.Errorf("fetching %s for strace: %v", stracePackager, err)
	}
	io.Copy(io.Discard, pulled)
	pulled.Close()

	script := `set -e
apk add --no-cache strace >/dev/null
mkdir -p /out
cp -L /usr/bin/strace /out/strace
cp -L /lib/ld-musl-*.so.1 /out/
for lib in $(ldd /usr/bin/strace | awk '/=>/ {print $3}'); do cp -Ln "$lib" /out/ 2>/dev/null || true; done`

	created, err := cli.ContainerCreate(ctx, &dockerContainer.Config{
		Image: stracePackager,
		Cmd:   []string{"sh", "-c", script},
	}, nil, nil, nil, "")
	if err != nil {
		return nil, err
	}
	defer cli.ContainerRemove(ctx, created.ID, dockerContainer.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, created.ID, dockerContainer.StartOptions{}); err != nil {
		return nil, err
	}
	waited, errs := cli.ContainerWait(ctx, created.ID, dockerContainer.WaitConditionNotRunning)
	select {
	case result := <-waited:
		if result.StatusCode != 0 {
			return nil, fmt.Errorf("getting strace out of %s stopped with %d", stracePackager, result.StatusCode)
		}
	case err := <-errs:
		return nil, err
	}

	reader, _, err := cli.CopyFromContainer(ctx, created.ID, "/out/.")
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.OpenFile(filepath.Join(dir, path.Base(header.Name)), os.O_CREATE|os.O_WRONLY, 0755)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			return nil, err
		}
	}

	loaders, _ := filepath.Glob(filepath.Join(dir, "ld-musl-*.so.1"))
	if len(loaders) == 0 {
		return nil, fmt.Errorf("no musl loader came with strace")
	}
	if _, err := os.Stat(filepath.Join(dir, "strace")); err != nil {
		return nil, fmt.Errorf("strace did not come out of %s", stracePackager)
	}

	// created ahead so that a program that is not root can write to it
	if err := os.WriteFile(filepath.Join(dir, "files"), nil, 0666); err != nil {
		return nil, err
	}
	os.Chmod(filepath.Join(dir, "files"), 0666)

	return &tracer{dir: dir, loader: filepath.Base(loaders[0])}, nil
}

// wrap puts strace in front of what the image starts. It follows every process and thread the
// program makes and writes down the calls that name a file.
func (t *tracer) wrap(argv []string) []string {
	wrapped := []string{
		traceMount + "/" + t.loader, "--library-path", traceMount, traceMount + "/strace",
		"-f", "-qq", "-s", "4096", "-e", "trace=file", "-e", "signal=none",
		"-o", traceMount + "/files", "--",
	}
	return append(wrapped, argv...)
}

func (t *tracer) bind() string {
	return t.dir + ":" + traceMount
}

// trace is what strace wrote down.
func (t *tracer) trace() []byte {
	raw, _ := os.ReadFile(filepath.Join(t.dir, "files"))
	return raw
}

var traceCall = regexp.MustCompile(`^(?:\[pid +\d+\] +|\d+ +)([a-z_0-9]+)\(`)

// firstQuoted reads the path a call names: its first argument, or the one after a descriptor.
func firstQuoted(rest string) (name string, after string, relativeToCwd bool, ok bool) {
	relativeToCwd = true
	if strings.HasPrefix(rest, "AT_FDCWD, ") {
		rest = rest[len("AT_FDCWD, "):]
	} else if !strings.HasPrefix(rest, "\"") {
		// the first argument is a descriptor: a path that is not absolute has no telling
		relativeToCwd = false
		if comma := strings.Index(rest, ", "); comma >= 0 {
			rest = rest[comma+2:]
		}
	}
	if !strings.HasPrefix(rest, "\"") {
		return "", "", false, false
	}

	end := 1
	for end < len(rest) && rest[end] != '"' {
		if rest[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(rest) {
		return "", "", false, false
	}
	name = rest[1:end]
	if unquoted, err := strconv.Unquote("\"" + name + "\""); err == nil {
		name = unquoted
	}

	return name, rest[end+1:], relativeToCwd, true
}

// resultOf is what a call came back with, and whether the trace has it.
func resultOf(after string) (string, bool) {
	at := strings.LastIndex(after, ") = ")
	if at < 0 {
		return "", false
	}
	result := after[at+len(") = "):]
	if space := strings.IndexAny(result, " \t"); space >= 0 {
		result = result[:space]
	}
	return result, true
}

// touchedPaths reads what strace wrote and returns the files named by the calls that found them.
// A call that came back with an error found nothing; one the trace cut in two - unfinished, with
// its end written on another line - is taken, since a file looked for is a file that may be needed.
// A path that is not absolute is read against workingDir when it was relative to the working
// directory, and left out when it was relative to a file descriptor that is not known here.
func touchedPaths(trace []byte, workingDir string) (paths []string, executed []string) {
	seen := map[string]bool{}
	ran := map[string]bool{}

	for _, line := range strings.Split(string(trace), "\n") {
		call := traceCall.FindStringSubmatch(line)
		if call == nil {
			continue
		}

		name, after, relativeToCwd, ok := firstQuoted(line[len(call[0]):])
		if !ok {
			continue
		}
		if !strings.Contains(after, "<unfinished ...>") {
			result, found := resultOf(after)
			if !found || strings.HasPrefix(result, "-") {
				continue
			}
		}

		if !strings.HasPrefix(name, "/") {
			if !relativeToCwd || workingDir == "" || name == "" {
				continue
			}
			name = path.Join(workingDir, name)
		}
		seen[path.Clean(name)] = true
		if call[1] == "execve" {
			ran[path.Clean(name)] = true
		}
	}

	for p := range seen {
		paths = append(paths, p)
	}
	for p := range ran {
		executed = append(executed, p)
	}
	sort.Strings(paths)
	sort.Strings(executed)

	return paths, executed
}

// notCarried says whether a path is one the kernel of the machine makes or the container runtime
// puts in, which no package has to bring.
func notCarried(p string) bool {
	for _, prefix := range []string{"/proc", "/sys", "/dev", traceMount} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// copyTouched copies the files that were opened out of the container into sysroot. A symbolic link
// is made as a link and its target is carried too, since the program reached the target through it.
func copyTouched(ctx context.Context, cli *dockerClient.Client, containerID string, sysroot string, paths []string, verbose bool) {
	done := map[string]bool{}
	queue := append([]string{}, paths...)

	for len(queue) > 0 {
		p := path.Clean(queue[0])
		queue = queue[1:]
		if done[p] || notCarried(p) {
			continue
		}
		done[p] = true

		reader, stat, err := cli.CopyFromContainer(ctx, containerID, p)
		if err != nil {
			// a file that was looked for and was there at the time, but is not any more
			if verbose {
				fmt.Printf("not carried %s: %v\n", p, err)
			}
			continue
		}

		target := filepath.Join(sysroot, p)
		tr := tar.NewReader(reader)
		header, err := tr.Next()
		if err != nil {
			reader.Close()
			continue
		}

		switch {
		case stat.Mode&os.ModeSymlink != 0 || header.Typeflag == tar.TypeSymlink:
			link := header.Linkname
			if link == "" {
				link = stat.LinkTarget
			}
			if os.MkdirAll(path.Dir(target), 0755) == nil {
				os.Symlink(link, target)
			}
			if !strings.HasPrefix(link, "/") {
				link = path.Join(path.Dir(p), link)
			}
			queue = append(queue, link)
		case header.Typeflag == tar.TypeDir:
			os.MkdirAll(target, 0755)
		case header.Typeflag == tar.TypeReg:
			if os.MkdirAll(path.Dir(target), 0755) != nil {
				break
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)|0600)
			if err != nil {
				break
			}
			io.Copy(out, tr)
			out.Close()
		}
		reader.Close()
	}
}

// inSysroot follows the symbolic links of a path that was carried, inside the sysroot rather than
// on this machine, and gives the path of the file they lead to.
func inSysroot(sysroot string, p string) string {
	for i := 0; i < 16; i++ {
		info, err := os.Lstat(filepath.Join(sysroot, p))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return p
		}
		link, err := os.Readlink(filepath.Join(sysroot, p))
		if err != nil {
			return p
		}
		if !strings.HasPrefix(link, "/") {
			link = path.Join(path.Dir(p), link)
		}
		p = path.Clean(link)
	}
	return p
}

// interpreterOf is the file the kernel loads to run a program: the dynamic loader named by an ELF
// file, or the program a script names after #!. A file that names none, a static binary or a
// library, gives "".
func interpreterOf(file string) string {
	if f, err := elf.Open(file); err == nil {
		defer f.Close()
		if section := f.Section(".interp"); section != nil {
			if raw, err := section.Data(); err == nil {
				return strings.TrimRight(string(raw), "\x00")
			}
		}
		return ""
	}

	in, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer in.Close()
	head := make([]byte, 256)
	n, _ := in.Read(head)
	if n < 2 || head[0] != '#' || head[1] != '!' {
		return ""
	}
	line := strings.SplitN(string(head[2:n]), "\n", 2)[0]
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// carryInterpreters copies, for each file that was executed, the interpreter the kernel loads for
// it, then the interpreter of that one if it has one. It is the second half of the rule.
func carryInterpreters(ctx context.Context, cli *dockerClient.Client, containerID string, sysroot string, executed []string, verbose bool) {
	done := map[string]bool{}
	queue := append([]string{}, executed...)

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		interp := interpreterOf(filepath.Join(sysroot, inSysroot(sysroot, p)))
		if interp == "" || done[interp] {
			continue
		}
		done[interp] = true

		if verbose {
			fmt.Printf("%s is run by %s\n", p, interp)
		}
		copyTouched(ctx, cli, containerID, sysroot, []string{interp}, verbose)
		queue = append(queue, interp)
	}
}

// imageProgram is what an image starts when it is not told otherwise, and the directory and
// environment it starts it in.
func imageProgram(ctx context.Context, cli *dockerClient.Client, image string) (argv []string, workingDir string, env []string, err error) {
	inspected, err := cli.ImageInspect(ctx, image)
	if err != nil {
		return nil, "", nil, err
	}
	config := inspected.Config
	if config == nil {
		return nil, "", nil, fmt.Errorf("%s has no configuration", image)
	}
	return append(append([]string{}, config.Entrypoint...), config.Cmd...), config.WorkingDir, config.Env, nil
}

// whereIs finds a program the way a shell would, among the files already carried.
func whereIs(sysroot string, program string, env []string) string {
	if strings.HasPrefix(program, "/") {
		return program
	}
	searched := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			searched = e[len("PATH="):]
		}
	}
	for _, dir := range strings.Split(searched, ":") {
		candidate := path.Join(dir, program)
		if _, err := os.Stat(filepath.Join(sysroot, candidate)); err == nil {
			return candidate
		}
	}
	return program
}

// ExtractFromDockerImageByTrace creates a package from a docker image by running it and carrying the
// files it opens. The program is the one the image starts, or targetExecutable followed by args.
func ExtractFromDockerImageByTrace(imageName string, packageName string, parch string, targetExecutable string, quiet bool, verbose bool, seconds int, args []string) (string, string) {
	var version string
	if packageName == "" {
		name, tag, err := ImageNameToPackageNameAndVersion(imageName)
		if err != nil {
			log.Fatal(err)
		}
		version = tag
		packageName = strings.TrimRight(name+"_"+version, "_")
	}
	if version == "" {
		version = "latest"
	}

	ctx := context.Background()
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatal(err)
	}

	if !strings.Contains(imageName, ":") {
		imageName += ":latest"
	}
	if _, err := cli.ImageInspect(ctx, imageName); err != nil {
		pulled, err := cli.ImagePull(ctx, imageName, dockerImage.PullOptions{})
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(io.Discard, pulled)
		pulled.Close()
	}

	argv, workingDir, env, err := imageProgram(ctx, cli, imageName)
	if err != nil {
		log.Fatal(err)
	}
	if targetExecutable != "" {
		argv = append([]string{targetExecutable}, args...)
	} else {
		argv = append(argv, args...)
	}
	if len(argv) == 0 {
		log.Fatalf("%s starts nothing: say what to run with --file", imageName)
	}

	tr, err := prepareTracer(ctx, cli)
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tr.dir)

	created, err := cli.ContainerCreate(ctx, &dockerContainer.Config{
		Image:      imageName,
		Entrypoint: tr.wrap(argv),
		Cmd:        []string{},
	}, &dockerContainer.HostConfig{
		Binds:       []string{tr.bind()},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
	}, nil, nil, "")
	if err != nil {
		log.Fatal(err)
	}
	defer cli.ContainerRemove(ctx, created.ID, dockerContainer.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, created.ID, dockerContainer.StartOptions{}); err != nil {
		log.Fatal(err)
	}

	// a server goes on running, a program that prints and stops does not: either way what it opened
	// in this time is what it needs
	waited, errs := cli.ContainerWait(ctx, created.ID, dockerContainer.WaitConditionNotRunning)
	select {
	case <-waited:
	case <-errs:
	case <-time.After(time.Duration(seconds) * time.Second):
	}
	// the list is written as the program goes, so what it has opened so far is in it
	touched, executed := touchedPaths(tr.trace(), workingDir)
	if len(touched) == 0 {
		log.Fatalf("%s opened nothing in %d seconds", imageName, seconds)
	}
	if verbose {
		fmt.Printf("the program opened %d files\n", len(touched))
	}

	tempDirectory, err := os.MkdirTemp("", "*")
	if err != nil {
		log.Fatal(err)
	}
	sysroot := tempDirectory + "/sysroot"
	if err := os.MkdirAll(sysroot, 0755); err != nil {
		log.Fatal(err)
	}

	copyTouched(ctx, cli, created.ID, sysroot, touched, verbose)
	carryInterpreters(ctx, cli, created.ID, sysroot, executed, verbose)

	program := whereIs(sysroot, argv[0], env)
	if _, err := os.Stat(filepath.Join(sysroot, program)); err != nil {
		log.Fatalf("%s did not open %s: say it with --file", imageName, argv[0])
	}

	c := &types.Config{
		Program: program,
		Args:    append([]string{program}, argv[1:]...),
		Version: version,
	}
	// the program was watched with the environment of the image, so it is run with it
	c.Env = map[string]string{}
	for _, e := range env {
		if name, value, found := strings.Cut(e, "="); found {
			c.Env[name] = value
		}
	}
	if workingDir != "" && workingDir != "/" {
		c.Env["PWD"] = workingDir
	}

	manifest, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path.Join(tempDirectory, "package.manifest"), manifest, 0666); err != nil {
		log.Panic(err)
	}

	packageDirectory := MovePackageFiles(tempDirectory, path.Join(api.LocalPackagesRoot, parch, packageName))

	return packageName, packageDirectory
}
