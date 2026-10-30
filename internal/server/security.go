package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Security baseline (ADR 0017): cross-origin form posts are refused, every
// response carries a strict CSP, and an optional single password protects the
// whole app. There are deliberately no user accounts. This is one business's
// books, run by one person or a small team who share the password
// (CLAUDE.md: no multi-tenant auth).

// Option configures the server.
type Option func(*Server)

// WithPassword turns the login on. An empty password leaves the app open,
// which is right for localhost and for a reverse proxy that already
// authenticates. The binary warns at startup when that looks unintended.
func WithPassword(password string) Option {
	return func(s *Server) {
		if password == "" {
			return
		}
		// The session key is derived from the password: changing the password
		// invalidates every session, and a restart does not.
		mac := hmac.New(sha256.New, []byte("kmu-buchhaltung session v1"))
		mac.Write([]byte(password))
		s.password = password
		s.sessionKey = mac.Sum(nil)
	}
}

const (
	sessionCookie = "kmu_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// contentSecurityPolicy allows only the app's own scripts, styles and images:
// no inline script, no inline style attribute (ADR 0013 keeps the templates
// free of both), no framing, and forms may only post back to the app.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// secure wraps the app with the security middleware, outermost first:
// headers on everything, then cross-origin protection, then the login gate.
func (s *Server) secure(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, s.T("Anfrage von einer fremden Seite abgelehnt."), http.StatusForbidden)
	}))
	return securityHeaders(cop.Handler(s.requireLogin(next)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(self), geolocation=(), microphone=()")
		next.ServeHTTP(w, r)
	})
}

// publicPath is what stays reachable without a session: the health check for
// the orchestrator, the static shell the login page itself needs, and the
// login.
func publicPath(p string) bool {
	return p == "/healthz" || p == "/login" || p == "/sw.js" || strings.HasPrefix(p, "/static/")
}

func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.password == "" || publicPath(r.URL.Path) || s.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		http.Error(w, s.T("Bitte zuerst anmelden."), http.StatusUnauthorized)
	})
}

// A session cookie is "<unix expiry>.<hex HMAC of the expiry>". There is no
// server-side session table, since there is only one identity to remember.
func (s *Server) sign(expiry int64) string {
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(strconv.FormatInt(expiry, 10)))
	return strconv.FormatInt(expiry, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	exp, _, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false
	}
	return hmac.Equal([]byte(s.sign(expiry)), []byte(c.Value))
}

// loginThrottle slows password guessing: every failed attempt costs the next
// attempt a second, process-wide. That is crude, but enough for a single
// shared password behind which sits one business.
var loginThrottle sync.Mutex

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.password == "" || s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login.html", pageData{Form: map[string]string{"next": safeNext(r.URL.Query().Get("next"))}})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.password == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	next := safeNext(r.FormValue("next"))

	loginThrottle.Lock()
	ok := subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(s.password)) == 1
	if !ok {
		time.Sleep(time.Second)
	}
	loginThrottle.Unlock()

	if !ok {
		s.render(w, http.StatusUnauthorized, "login.html", pageData{
			Error: s.T("Das Passwort stimmt nicht."), Form: map[string]string{"next": next},
		})
		return
	}
	expiry := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.sign(expiry), Path: "/",
		Expires: time.Unix(expiry, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode,
		// Behind a TLS-terminating proxy the request itself is plain HTTP;
		// the proxy's header is what says the browser used HTTPS.
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext only allows a local path as the post-login target, so the login
// page cannot be used to bounce a user to another site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}
