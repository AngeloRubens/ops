package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	dockerClient "github.com/docker/docker/client"
)

// A program opens some of what it needs only when it is asked for something: the certificate on the
// first connection, the template on the first page, the plugin on the first use. Watched for a
// minute and left alone, it has not opened them. The person who knows what the program is for can
// ask it, and what it opens to answer is then in the list too.

// exerciseSpec is what is done to the program once it is up, while it is still being watched.
type exerciseSpec struct {
	// urls are requests made to the program: a path is taken against the first port the image
	// exposes, and a url with the host "container" is taken against the container, whichever
	// port it says.
	urls []string

	// command is run on this side with OPS_HOST and OPS_PORT naming the container, for a program
	// that does not speak http.
	command string

	// port is the first port the image exposes, 80 when it exposes none.
	port string
}

func (e *exerciseSpec) empty() bool {
	return e == nil || (len(e.urls) == 0 && e.command == "")
}

// exerciseURL is the address a request is made to: raw taken against the container at host, and
// at port when it names none.
func exerciseURL(raw string, host string, port string) (string, error) {
	if port == "" {
		port = "80"
	}
	if strings.HasPrefix(raw, "/") {
		return "http://" + net.JoinHostPort(host, port) + raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%q is neither a path nor a url", raw)
	}
	if u.Hostname() != "container" {
		return "", fmt.Errorf("%q does not name the container: use the host \"container\" or a path", raw)
	}
	if u.Port() != "" {
		port = u.Port()
	}
	u.Host = net.JoinHostPort(host, port)

	return u.String(), nil
}

// run makes the requests and runs the command. What fails is said and does not stop the build: the
// package is made from whatever was seen.
func (e *exerciseSpec) run(ctx context.Context, cli *dockerClient.Client, container string, verbose bool) {
	inspected, err := cli.ContainerInspect(ctx, container)
	if err != nil || inspected.NetworkSettings == nil {
		fmt.Printf("warning: could not find the address of the container to call it: %v\n", err)
		return
	}
	host := inspected.NetworkSettings.IPAddress
	if host == "" {
		for _, network := range inspected.NetworkSettings.Networks {
			if network.IPAddress != "" {
				host = network.IPAddress
				break
			}
		}
	}
	if host == "" {
		fmt.Println("warning: the container has no address to call")
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	for _, raw := range e.urls {
		address, err := exerciseURL(raw, host, e.port)
		if err != nil {
			fmt.Printf("warning: %v\n", err)
			continue
		}
		response, err := client.Get(address)
		if err != nil {
			fmt.Printf("warning: calling %s: %v\n", raw, err)
			continue
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if verbose {
			fmt.Printf("called %s: %s\n", raw, response.Status)
		}
	}

	if e.command != "" {
		port := e.port
		if port == "" {
			port = "80"
		}
		commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		command := exec.CommandContext(commandCtx, "sh", "-c", e.command)
		command.Env = append(os.Environ(), "OPS_HOST="+host, "OPS_PORT="+port)
		out, err := command.CombinedOutput()
		if err != nil {
			fmt.Printf("warning: running %q: %v\n", e.command, err)
		}
		if verbose {
			fmt.Printf("ran %q: %s\n", e.command, strings.TrimSpace(string(out)))
		}
	}
}
