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

// tokenParam is the query key of the link that opens the page; servers link prints one.
const tokenParam = "token"

// cookieName is the token's cookie for the server at host, <product>-token-<port>: a browser sends one host's cookies to every port.
func (a App) cookieName(host string) string {
	if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
		return a.ID + "-token-" + port
	}
	return a.ID + "-token"
}

// pageLink is the link that opens the page of the server at addr; the server swaps the token for a cookie.
func pageLink(addr, token string) string {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + addr + "/?" + tokenParam + "=" + url.QueryEscape(token)
}

// bearer is the token of an Authorization: Bearer header, the one way every app's clients send it; empty without one.
func bearer(r *http.Request) string {
	scheme, t, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(t)
}

func (a App) guard(dir string, hosts []string, name string, next http.Handler) http.Handler {
	// A refusal is in the contract the reader asked for when the app serves it, so a reader of an older contract can read it (CT-RULE-1); otherwise in the default.
	refuse := func(w http.ResponseWriter, r *http.Request, status int, p *Problem) {
		// Only an API reader asks a contract, as choose reads it; the page gets the default.
		asked := ""
		if strings.HasPrefix(r.URL.Path, "/api/") {
			asked = strings.TrimSpace(r.Header.Get(ContractHeader))
		}
		contract, err := a.Contract(asked)
		if err != nil {
			contract, _ = a.Contract("")
		}
		writeEnvelope(w, status, a.Failure(name, contract, p))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if !allowedHost(r.Host, hosts) {
			refuse(w, r, http.StatusForbidden, Fail(TypeRefused, ReasonHost, fmt.Sprintf("%s answers on this machine's own address only; serve --host <name> names another", a.command())))
			return
		}
		page := !strings.HasPrefix(r.URL.Path, "/api/")
		// Read on every request, so a renewed token counts at once; a file that cannot be trusted lets nobody in.
		token, err := a.readToken(dir)
		if err != nil {
			if page {
				writePage(w, http.StatusInternalServerError, a.command(), err.Error())
				return
			}
			refuse(w, r, http.StatusInternalServerError, Fail(TypeFailed, ReasonConfigUnreadable, err.Error()))
			return
		}
		valid := func(t string) bool { return subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 }
		cookie := a.cookieName(r.Host)
		if page {
			if r.URL.Query().Has(tokenParam) {
				if !valid(r.URL.Query().Get(tokenParam)) {
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
			refuse(w, r, http.StatusForbidden, Fail(TypeRefused, ReasonOrigin, fmt.Sprintf("%s takes an API request only from its own page", a.command())))
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			refuse(w, r, http.StatusForbidden, Fail(TypeRefused, ReasonOrigin, fmt.Sprintf("%s takes an API request only from its own page", a.command())))
			return
		}
		t := bearer(r)
		if c, err := r.Cookie(cookie); t == "" && err == nil {
			t = c.Value
		}
		if !valid(t) {
			refuse(w, r, http.StatusUnauthorized, Fail(TypeRefused, ReasonToken, fmt.Sprintf(
				"%s takes an API request only with its token, as Authorization: Bearer <token> or in the cookie the page's link left; `%s servers link` prints the link",
				a.command(), a.command())))
			return
		}
		if r.ContentLength != 0 && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "application/json" {
				refuse(w, r, http.StatusUnsupportedMediaType, Fail(TypeRefused, ReasonNotJSON, fmt.Sprintf("%s takes a request body as JSON only (Content-Type: application/json)", a.command())))
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

// writeLinkPage tells a browser without a valid cookie, after a renew too, where its link is, and holds no secret.
func (a App) writeLinkPage(w http.ResponseWriter) {
	c := a.command()
	writePage(w, http.StatusUnauthorized, c, "Open this page from its link: run "+c+" servers link and open the link it prints.")
}

func writePage(w http.ResponseWriter, status int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><title>%s</title>\n<p>%s</p>\n", html.EscapeString(title), html.EscapeString(text))
}

// withoutToken is u on this server, as a path, with the token taken out of its query.
func withoutToken(u *url.URL) string {
	path := u.EscapedPath()
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		path = "/"
	}
	q := u.Query()
	q.Del(tokenParam)
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
