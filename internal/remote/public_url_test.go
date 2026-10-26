package remote

import "testing"

func TestTunnelPublicURLLegacyCustomDomain(t *testing.T) {
	c := Config{Tunnel: &TunnelConfig{CustomDomain: "public.remote.test"}}
	got, err := c.TunnelPublicURL()
	if err != nil || got != "https://public.remote.test" {
		t.Fatalf("legacy public URL=%q error=%v", got, err)
	}
}

func TestTunnelPublicURLValidation(t *testing.T) {
	for _, tc := range []struct{ raw, domain, want string }{
		{"https://public.remote.test/", "public.remote.test", "https://public.remote.test"},
		{"http://public.remote.test:8080", "public.remote.test", "http://public.remote.test:8080"},
		{"https://public.remote.test", "", "https://public.remote.test"},
		{"", "", ""},
		{"https://foreign.remote.test", "public.remote.test", ""},
		{"https://public.remote.test/path", "public.remote.test", ""},
		{"https://public.remote.test?", "public.remote.test", ""},
		{"https://user@public.remote.test", "public.remote.test", ""},
		{"https://public.remote.test:65536", "public.remote.test", ""},
		{"https://public.remote.test#", "public.remote.test", ""},
	} {
		t.Run(tc.raw+tc.domain, func(t *testing.T) {
			got, err := (Config{Tunnel: &TunnelConfig{PublicURL: tc.raw, CustomDomain: tc.domain, Subdomain: "example"}}).TunnelPublicURL()
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid public URL accepted")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("URL=%q error=%v", got, err)
			}
		})
	}
}
