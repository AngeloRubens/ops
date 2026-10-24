package cmd

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	dockerContainer "github.com/docker/docker/api/types/container"
	dockerImage "github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"
)

// What a program needs to start and to keep running is what it opens while it does, and nothing
// says that better than watching it. An image run to see what it starts is run under strace, which
// writes down every file the program and its children look for and find, from the first
// instruction on. The libraries a program is linked against and the ones it loads by name, the
// configuration it reads, the data it opens: all of it is in that list, and what is not in it is
// what the package can do without.

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
func prepareTracer(ctx context.Context, cli *dockerClient.Client, verbose bool) (*tracer, error) {
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

	if verbose {
		fmt.Printf("tracing with strace from %s in %s\n", stracePackager, dir)
	}

	return &tracer{dir: dir, loader: filepath.Base(loaders[0])}, nil
}

// wrap puts strace in front of what the image starts. It follows every process and thread the
// program makes and writes down the calls that name a file.
func (t *tracer) wrap(entrypoint []string) []string {
	wrapped := []string{
		traceMount + "/" + t.loader, "--library-path", traceMount, traceMount + "/strace",
		"-f", "-qq", "-s", "4096", "-e", "trace=file,process", "-e", "signal=none",
		"-o", traceMount + "/files", "--",
	}
	return append(wrapped, entrypoint...)
}

func (t *tracer) bind() string {
	return t.dir + ":" + traceMount
}

// trace is what strace wrote down.
func (t *tracer) trace() []byte {
	raw, _ := os.ReadFile(filepath.Join(t.dir, "files"))
	return raw
}

func (t *tracer) remove() {
	os.RemoveAll(t.dir)
}

// isTracer reports whether a process is strace, or the loader it was started through, which stand
// in front of the program being looked for and are not it.
func isTracer(name string) bool {
	return name == "strace" || strings.HasPrefix(name, "ld-musl-")
}

var traceCall = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)([a-z_0-9]+)\(`)
var traceResumed = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)<\.\.\. ([a-z_0-9]+) resumed>`)

// spawns are the calls that make a process or a thread.
var spawns = []string{"clone", "clone3", "fork", "vfork"}

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

// resultOf is what a call came back with, and whether the trace has it: a call cut in two by another
// thread has its end on a line of its own.
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

type traceFile struct {
	pid  string
	line int
	path string
}

// touchedPaths reads what strace wrote and returns the files named by the calls that found them.
// A call that came back with an error found nothing; one the trace cut in two - unfinished, with
// its end written on another line - is taken, since a file looked for is a file that may be needed.
// A path that is not absolute is read against workingDir when it was relative to the working
// directory, and left out when it was relative to a file descriptor that is not known here.
//
// A launcher script runs tools - sed, find, a shell - before it hands over, and a unikernel runs no
// script, so what they opened is not what the program needs. When programs says which files the
// program is, only what the processes that became it opened, and the processes they made after, is
// taken. A trace in which none of them was started is read whole.
func touchedPaths(trace []byte, workingDir string, programs []string) []string {
	var files []traceFile
	firstRun := map[string]int{}
	// where a process made by the program turned into something else
	leftAt := map[string]int{}
	type child struct {
		parent string
		at     int
	}
	children := map[string]child{}
	ran := false

	for i, line := range strings.Split(string(trace), "\n") {
		if m := traceResumed.FindStringSubmatch(line); m != nil {
			pid := m[1] + m[2]
			if contains(spawns, m[3]) {
				if result, ok := resultOf(line); ok {
					if n, err := strconv.Atoi(result); err == nil && n > 0 {
						children[result] = child{parent: pid, at: i}
					}
				}
			}
			continue
		}

		call := traceCall.FindStringSubmatch(line)
		if call == nil {
			continue
		}
		pid, name := call[1]+call[2], call[3]
		rest := line[len(call[0]):]

		if contains(spawns, name) {
			if result, ok := resultOf(rest); ok {
				if n, err := strconv.Atoi(result); err == nil && n > 0 {
					children[result] = child{parent: pid, at: i}
				}
			}
			continue
		}

		path, after, relativeToCwd, ok := firstQuoted(rest)
		if !ok {
			continue
		}
		if !strings.Contains(after, "<unfinished ...>") {
			result, found := resultOf(after)
			if !found || strings.HasPrefix(result, "-") {
				continue
			}
		}

		if !strings.HasPrefix(path, "/") {
			if !relativeToCwd || workingDir == "" || path == "" {
				continue
			}
			path = pathJoin(workingDir, path)
		}
		path = pathClean(path)

		if name == "execve" {
			if contains(programs, path) {
				if _, seen := firstRun[pid]; !seen {
					firstRun[pid] = i
				}
				ran = true
			} else if _, seen := leftAt[pid]; !seen {
				leftAt[pid] = i
			}
		}
		files = append(files, traceFile{pid: pid, line: i, path: path})
	}

	var counts func(pid string, line int, depth int) bool
	counts = func(pid string, line int, depth int) bool {
		if at, ok := firstRun[pid]; ok && line >= at {
			return true
		}
		if c, ok := children[pid]; ok && depth < 64 {
			// what the program made and then turned into a tool of its own is not the program
			if left, ok := leftAt[pid]; ok && line > left {
				return false
			}
			return counts(c.parent, c.at, depth+1)
		}
		return false
	}

	seen := map[string]bool{}
	for _, f := range files {
		if ran && !counts(f.pid, f.line, 0) {
			continue
		}
		seen[f.path] = true
	}

	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	return paths
}

func pathJoin(a string, b string) string { return path.Join(a, b) }
func pathClean(p string) string          { return path.Clean(p) }
