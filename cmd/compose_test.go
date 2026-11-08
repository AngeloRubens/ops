package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanovms/ops/types"
)

// dnsStub stands in for the dns of the session and keeps the requests it is sent. dnsPort is set
// to its port for the length of the test.
func dnsStub(t *testing.T) (requests func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.RequestURI())
		mu.Unlock()
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	old := dnsPort
	dnsPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	t.Cleanup(func() { dnsPort = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// machine makes findBridgedIP answer "" for the first misses calls and ip after that, and the
// polling instant.
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
	requests := dnsStub(t)
	machine(t, 0, "192.168.33.5")
	com := Compose{config: &types.Config{}}
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
	requests := dnsStub(t)
	machine(t, 1<<30, "")
	com := Compose{config: &types.Config{}}
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
	requests := dnsStub(t)
	calls := machine(t, 40, "192.168.33.9")
	com := Compose{config: &types.Config{}}
	if err := com.registerService("127.0.0.1", "db", "42", "nonce"); err != nil {
		t.Fatalf("err = %v after %d calls", err, calls())
	}
	if got := requests(); len(got) != 1 {
		t.Errorf("requests = %q", got)
	}
}

// The dns is not there: that is an error to report, not a nil pointer.
func TestAddDNSWithoutADNSIsAnError(t *testing.T) {
	old := dnsPort
	dnsPort = "1"
	t.Cleanup(func() { dnsPort = old })
	com := Compose{config: &types.Config{}}
	if err := com.addDNS("127.0.0.1", "db", "192.168.33.5", "nonce"); err == nil {
		t.Fatal("no error")
	}
}
