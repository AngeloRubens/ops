package lepton

import (
	"testing"

	"github.com/nanovms/ops/types"
)

func TestResolvConfHasTheSearchLineOnlyWhenThereAreDomains(t *testing.T) {
	tests := []struct {
		name string
		c    types.Config
		want string
	}{
		{"name servers only", types.Config{NameServers: []string{"8.8.8.8"}}, "nameserver  8.8.8.8\n"},
		{"with a search domain", types.Config{NameServers: []string{"192.168.33.2"}, SearchDomains: []string{"service"}}, "nameserver  192.168.33.2\nsearch service\n"},
		{"two domains", types.Config{NameServers: []string{"1.1.1.1", "8.8.8.8"}, SearchDomains: []string{"a", "b"}}, "nameserver  1.1.1.1\nnameserver  8.8.8.8\nsearch a b\n"},
		{"nothing", types.Config{}, ""},
	}
	for _, tt := range tests {
		if got := string(resolvConf(&tt.c)); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
