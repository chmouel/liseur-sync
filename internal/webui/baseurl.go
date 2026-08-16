package webui

import (
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
)

// serverBaseURL is this server's absolute origin as the browser
// reached it, for the URLs a person has to retype into another program.
//
// It is derived from the request rather than configured, because the
// answer has to be the one that works from where they are standing: a
// server reachable on a LAN address, a Tailscale name and a public
// hostname has three correct answers, and the useful one is whichever
// they are looking at the page through.
//
// The scheme goes through auth.IsSecure so that a deployment behind a
// TLS-terminating proxy prints https rather than the http it is spoken
// to over — and so that an X-Forwarded-Proto from an untrusted peer
// cannot make this page advertise a scheme the server does not answer.
func (s *Server) serverBaseURL(r *http.Request) string {
	scheme := "http"
	if auth.IsSecure(r, s.Cfg) {
		scheme = "https"
	}
	return scheme + "://" + requestHost(r)
}
