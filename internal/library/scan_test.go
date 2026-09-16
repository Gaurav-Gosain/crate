package library

import "testing"

// Subsonic answers with HTTP 200 whether or not it did what was asked, so the
// body is the only place the truth lives. Every sync in this library's history
// reported "server rescanning" and then success while the scan was being
// refused for missing credentials.
func TestSubsonicErrorDetectsRefusal(t *testing.T) {
	body := []byte(`{"subsonic-response":{"status":"failed","version":"1.16.1","error":{"code":10,"message":"missing parameter: 'u'"}}}`)
	err := subsonicError(body)
	if err == nil {
		t.Fatal("a refused scan must be reported as an error")
	}
	if got := err.Error(); got != "scan refused: missing parameter: 'u' (code 10)" {
		t.Fatalf("got %q", got)
	}
}

func TestSubsonicErrorAcceptsSuccess(t *testing.T) {
	if err := subsonicError([]byte(`{"subsonic-response":{"status":"ok","version":"1.16.1"}}`)); err != nil {
		t.Fatalf("a successful scan must not error: %v", err)
	}
}

func TestSubsonicErrorReportsWrongCredentials(t *testing.T) {
	body := []byte(`{"subsonic-response":{"status":"failed","error":{"code":40,"message":"Wrong username or password"}}}`)
	err := subsonicError(body)
	if err == nil || err.Error() != "scan refused: Wrong username or password (code 40)" {
		t.Fatalf("got %v", err)
	}
}

func TestSubsonicErrorHandlesGarbage(t *testing.T) {
	if err := subsonicError([]byte("<html>502 Bad Gateway</html>")); err == nil {
		t.Fatal("an unparseable reply must not count as success")
	}
}
