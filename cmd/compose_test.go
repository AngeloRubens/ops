package cmd

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanovms/ops/types"
)

// roundTrip lets a function stand in for the network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body))}
}

// dnsStub is a client whose transport stands in for the dns of the session; it keeps the requests.
func dnsStub(status int) (client *http.Client, requests func() []string) {
	var mu sync.Mutex
	var got []string
	client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		got = append(got, r.URL.RequestURI())
		mu.Unlock()
		return reply(status, "ok"), nil
	})}
	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// machine makes findBridgedIP answer "" for the first misses calls and ip after that, and makes
// ipPollEvery a microsecond.
func machine(t *testing.T, misses int, ip string) (calls func() int) {
	t.Helper()
	oldFind, oldEvery := findBridgedIP, ipPollEvery
	n := 0
	findBridgedIP = func(string) string {
		n++
		if n <= misses {
			return ""
		}
		return ip
	}
	ipPollEvery = time.Microsecond
	t.Cleanup(func() { findBridgedIP, ipPollEvery = oldFind, oldEvery })
	return func() int { return n }
}

func TestRegisterServiceAddsTheRecordOfTheAddress(t *testing.T) {
	client, requests := dnsStub(http.StatusOK)
	machine(t, 0, "192.168.33.5")
	com := Compose{config: &types.Config{}, client: client}
	if err := com.registerService("127.0.0.1", "db", "42", "nonce"); err != nil {
		t.Fatal(err)
	}
	got := requests()
	if len(got) != 1 || got[0] != "/add?svc=db.service&ip=192.168.33.5" {
		t.Errorf("requests = %q", got)
	}
}

// A record with an empty address stays in the dns for the whole session: the name never resolves
// though the unikernel runs. Without an address nothing is added, and the error says so.
func TestRegisterServiceDoesNotAddARecordWithoutAnAddress(t *testing.T) {
	client, requests := dnsStub(http.StatusOK)
	machine(t, 1<<30, "")
	com := Compose{config: &types.Config{}, client: client}
	err := com.registerService("127.0.0.1", "db", "42", "nonce")
	if err == nil || !strings.Contains(err.Error(), "ip timeout") || !strings.Contains(err.Error(), "db") {
		t.Fatalf("err = %v", err)
	}
	if got := requests(); len(got) != 0 {
		t.Errorf("requests = %q, want none", got)
	}
}

// The address of a large image on a busy host turns up after more than ten seconds.
func TestRegisterServiceWaitsLongerThanTenSeconds(t *testing.T) {
	client, requests := dnsStub(http.StatusOK)
	calls := machine(t, 40, "192.168.33.9")
	com := Compose{config: &types.Config{}, client: client}
	if err := com.registerService("127.0.0.1", "db", "42", "nonce"); err != nil {
		t.Fatalf("err = %v after %d calls", err, calls())
	}
	if got := requests(); len(got) != 1 {
		t.Errorf("requests = %q", got)
	}
}

// The dns is not there: that is an error to report, not a nil pointer.
func TestAddDNSWithoutADNSIsAnError(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connect: connection refused")
	})}
	com := Compose{config: &types.Config{}, client: client}
	if err := com.addDNS("127.0.0.1", "db", "192.168.33.5", "nonce"); err == nil {
		t.Fatal("no error")
	}
}

// A request that cannot even be made is an error too.
func TestAddDNSWithAnImpossibleAddressIsAnError(t *testing.T) {
	client, requests := dnsStub(http.StatusOK)
	com := Compose{config: &types.Config{}, client: client}
	if err := com.addDNS("bad host", "db", "192.168.33.5", "nonce"); err == nil {
		t.Fatal("no error")
	}
	if got := requests(); len(got) != 0 {
		t.Errorf("requests = %q", got)
	}
}

// A dns that accepts the connection and never answers does not stop the session for ever: the
// client that ops makes for itself has a timeout, and the transport is one that waits for it.
func TestAddDNSGivesUpOnADNSThatDoesNotAnswer(t *testing.T) {
	client := &http.Client{Timeout: 50 * time.Millisecond, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	com := Compose{config: &types.Config{}, client: client}
	done := make(chan error, 1)
	go func() { done <- com.addDNS("127.0.0.1", "db", "192.168.33.5", "nonce") }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("addDNS did not give up")
	}
}

// The client of a session that was given none has a timeout.
func TestTheDefaultDNSClientHasATimeout(t *testing.T) {
	if got := (Compose{}).dnsClient().Timeout; got != dnsTimeout || got == 0 {
		t.Errorf("timeout = %v", got)
	}
}

// A dns that answers with anything but 200 did not add the record: that is an error too.
func TestAddDNSRefusedByTheDNSIsAnError(t *testing.T) {
	client, _ := dnsStub(http.StatusBadRequest)
	com := Compose{config: &types.Config{}, client: client}
	err := com.addDNS("127.0.0.1", "db", "192.168.33.5", "nonce")
	if err == nil || !strings.Contains(err.Error(), "dns answered") {
		t.Fatalf("error = %v", err)
	}
}
