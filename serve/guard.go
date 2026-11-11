package serve

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Another site the user visits can send requests to a port on this machine; the guard takes only the page's own.
// The token is never in a page: the link serve prints carries it once, and the guard swaps it for a cookie.

// TokenParam is the query key of the link that opens the page; servers link prints one.
const TokenParam = "token"

// TokenHeader is the header every API request carries the token in: X-<Product>-Token, such as X-Control-Room-Token.
func (a App) TokenHeader() string {
	parts := strings.Split(a.ID, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return "X-" + strings.Join(parts, "-") + "-Token"
}

// CookieName is the token's cookie for the server at host, <product>-token-<port>: a browser sends one host's cookies to every port.
func (a App) CookieName(host string) string {
	if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
		return a.ID + "-token-" + port
	}
	return a.ID + "-token"
}

// Link is the link that opens the page of the server at addr; the server swaps the token for a cookie.
func Link(addr, token string) string {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + addr + "/?" + TokenParam + "=" + url.QueryEscape(token)
}

func (a App) guard(token string, hosts []string, name string, next http.Handler) http.Handler {
	valid := func(t string) bool { return subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 }
	refuse := func(w http.ResponseWriter, status int, code, message string) {
		contract, _ := a.Contract("")
		writeEnvelope(w, status, a.Failure(name, contract, Fail(code, message)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if !allowedHost(r.Host, hosts) {
			refuse(w, http.StatusForbidden, CodeHost, fmt.Sprintf("%s answers on this machine's own address only; serve --host <name> names another", a.command()))
			return
		}
		cookie := a.CookieName(r.Host)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.URL.Query().Has(TokenParam) {
				if !valid(r.URL.Query().Get(TokenParam)) {
					a.writeLinkPage(w)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: cookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: overTLS(r)})
				writeOnward(w, withoutToken(r.URL))
				return
			}
			if c, err := r.Cookie(cookie); err != nil || !valid(c.Value) {
				a.writeLinkPage(w)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			refuse(w, http.StatusForbidden, CodeOrigin, fmt.Sprintf("%s takes an API request only from its own page", a.command()))
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			refuse(w, http.StatusForbidden, CodeOrigin, fmt.Sprintf("%s takes an API request only from its own page", a.command()))
			return
		}
		t := r.Header.Get(a.TokenHeader())
		if c, err := r.Cookie(cookie); t == "" && err == nil {
			t = c.Value
		}
		if !valid(t) {
			refuse(w, http.StatusUnauthorized, CodeToken, fmt.Sprintf(
				"%s takes an API request only with its token, in the %s header or the cookie the page's link left; `%s servers link` prints the link",
				a.command(), a.TokenHeader(), a.command()))
			return
		}
		if r.ContentLength != 0 && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "application/json" {
				refuse(w, http.StatusUnsupportedMediaType, CodeNotJSON, fmt.Sprintf("%s takes a request body as JSON only (Content-Type: application/json)", a.command()))
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// maxBody is the most a request body may hold.
const maxBody = 1 << 20

// overTLS reports whether the browser reached the server over HTTPS, itself or through a proxy, so the cookie is sent over HTTPS only.
func overTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// writeOnward sends the browser on to path from this page, not by a redirect: a Strict cookie set on a redirect that began on another site is not sent.
// location.replace also drops the link, with its token, from the history; the refresh is for a browser without scripts.
func writeOnward(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	p := html.EscapeString(path)
	js, _ := json.Marshal(path)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><script>location.replace(%s)</script><meta http-equiv="refresh" content="0;url=%s"><a href="%s">Open</a>`+"\n", js, p, p)
}

// writeLinkPage tells a browser without the cookie where its link is, and holds no secret.
func (a App) writeLinkPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	c := html.EscapeString(a.command())
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<p>Open this page from its link: the one <code>%s serve</code> printed, or <code>%s servers link</code>.</p>
`, c, c, c)
}

// withoutToken is u on this server, as a path, with the token taken out of its query.
func withoutToken(u *url.URL) string {
	path := u.EscapedPath()
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		path = "/"
	}
	q := u.Query()
	q.Del(TokenParam)
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// allowedHost reports whether a request's Host is this machine, or a name the server was told to answer to (DNS rebinding).
func allowedHost(hostport string, hosts []string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, h := range hosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}
