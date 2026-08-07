package amocrm

import "testing"

// TestSubdomainFromRefererValid covers the normal, expected shapes
// amoCRM's own redirect sends.
func TestSubdomainFromRefererValid(t *testing.T) {
	cases := map[string]string{
		"example.amocrm.ru":         "example",
		"sub-domain123.amocrm.ru":   "sub-domain123",
		"example.kommo.com":         "example",
		"https://example.amocrm.ru": "example",
		"http://example.amocrm.ru":  "example",
	}
	for referer, want := range cases {
		got, err := subdomainFromReferer(referer)
		if err != nil {
			t.Errorf("subdomainFromReferer(%q) unexpected error: %v", referer, err)
			continue
		}
		if got != want {
			t.Errorf("subdomainFromReferer(%q) = %q, want %q", referer, got, want)
		}
	}
}

// TestSubdomainFromRefererRejectsInjection locks in the fix for a real
// vulnerability caught during review: Callback is a PUBLIC,
// unauthenticated route, and the extracted subdomain gets interpolated
// directly into a URL that this server POSTs the platform's own amoCRM
// client_secret to (see amocrmapi.Client.exchangeToken). Before this
// fix, subdomainFromReferer only checked the suffix and non-emptiness,
// so a referer like "attacker.com/evil.amocrm.ru" produced the
// "subdomain" attacker.com/evil — which resolves to host attacker.com
// once interpolated into "https://%s.amocrm.ru/...", exfiltrating the
// shared platform secret to an attacker-chosen server. Every case here
// must return an error, not a subdomain.
func TestSubdomainFromRefererRejectsInjection(t *testing.T) {
	cases := []string{
		"attacker.com/evil.amocrm.ru",
		"attacker.com%2Fevil.amocrm.ru",
		"evil.amocrm.ru@attacker.com",
		"amocrm.ru",
		".amocrm.ru",
		"",
		"not-a-real-domain.com",
		"example.amocrm.ru.attacker.com",
		"exa mple.amocrm.ru",
	}
	for _, referer := range cases {
		got, err := subdomainFromReferer(referer)
		if err == nil {
			t.Errorf("subdomainFromReferer(%q) = %q, <nil>; want an error", referer, got)
		}
	}
}
