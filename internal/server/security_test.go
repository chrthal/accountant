package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/server"
	"github.com/chrthal/accountant/internal/store"
)

func newProtectedServer(t *testing.T, password string) http.Handler {
	t.Helper()
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return server.New(st, "test", server.WithPassword(password))
}

// Phase 12 acceptance, first half: a form post from another site is refused.
func TestCrossOriginPostIsRefused(t *testing.T) {
	h, _ := newTestServer(t)
	form := url.Values{"kind": {"customer"}, "name": {"Eindringling"}}

	req := httptest.NewRequest(http.MethodPost, "/contacts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/contacts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "books.local"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with a foreign Origin = %d, want 403", rec.Code)
	}

	// The app's own forms still work.
	req = httptest.NewRequest(http.MethodPost, "/contacts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin POST = %d, want 303", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newTestServer(t)
	rec := get(t, h, "/")
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "style-src 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Error("the page CSP allows inline code")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers = %v", rec.Header())
	}
}

// Phase 12 acceptance, second half: with a password, everything but the
// health check and the static shell needs a session.
func TestLoginGate(t *testing.T) {
	h := newProtectedServer(t, "geheim")

	for path, want := range map[string]int{
		"/healthz": 200, "/static/app.css": 200, "/sw.js": 200, "/login": 200,
		"/": 303, "/entries": 303, "/export/csv": 303,
	} {
		if rec := get(t, h, path); rec.Code != want {
			t.Errorf("GET %s without session = %d, want %d", path, rec.Code, want)
		}
	}
	if loc := get(t, h, "/entries?year=2026").Header().Get("Location"); loc != "/login?next=%2Fentries%3Fyear%3D2026" {
		t.Errorf("redirect = %q", loc)
	}
	if rec := post(t, h, "/contacts", url.Values{"name": {"x"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST without session = %d, want 401", rec.Code)
	}

	if rec := post(t, h, "/login", url.Values{"password": {"falsch"}}); rec.Code != http.StatusUnauthorized ||
		rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("wrong password = %d, cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}

	rec := post(t, h, "/login", url.Values{"password": {"geheim"}, "next": {"/entries"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entries" {
		t.Fatalf("login = %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie flags = %+v", cookie)
	}

	req := httptest.NewRequest(http.MethodGet, "/entries", nil)
	req.AddCookie(cookie)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "Abmelden") {
		t.Errorf("with session = %d (logout offered: %v)", got.Code, strings.Contains(got.Body.String(), "Abmelden"))
	}

	// A tampered cookie is worthless, and so is one from another password.
	forged := *cookie
	forged.Value = "9999999999." + strings.Repeat("0", 64)
	req = httptest.NewRequest(http.MethodGet, "/entries", nil)
	req.AddCookie(&forged)
	got = httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusSeeOther {
		t.Errorf("forged session = %d, want redirect to login", got.Code)
	}
	other := newProtectedServer(t, "anderes")
	req = httptest.NewRequest(http.MethodGet, "/entries", nil)
	req.AddCookie(cookie)
	got = httptest.NewRecorder()
	other.ServeHTTP(got, req)
	if got.Code != http.StatusSeeOther {
		t.Errorf("session from another password = %d", got.Code)
	}

	// The login cannot be used to redirect elsewhere.
	rec = post(t, h, "/login", url.Values{"password": {"geheim"}, "next": {"//evil.example/"}})
	if rec.Header().Get("Location") != "/" {
		t.Errorf("open redirect: %q", rec.Header().Get("Location"))
	}
}
