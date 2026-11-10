package cmd

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestComposeFileReadsDNSSearch(t *testing.T) {
	var y ComposeFile
	if err := yaml.Unmarshal([]byte("dns_search:\n  - service\npackages:\n  - pkg: db\n"), &y); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(y.DNSSearch, []string{"service"}) {
		t.Errorf("DNSSearch = %v", y.DNSSearch)
	}
	var none ComposeFile
	if err := yaml.Unmarshal([]byte("packages:\n  - pkg: db\n"), &none); err != nil || len(none.DNSSearch) != 0 {
		t.Errorf("without the key: %+v %v", none, err)
	}
}

func TestCheckDNSSearch(t *testing.T) {
	for _, ok := range [][]string{nil, {"service"}, {"a.b", "c"}} {
		if err := checkDNSSearch(ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a\nnameserver 1.1.1.1", "a;b", "a/b", "#x"} {
		if err := checkDNSSearch([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
