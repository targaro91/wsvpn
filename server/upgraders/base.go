package upgraders

import (
	"io"
	"net/http"

	"github.com/Doridian/wsvpn/shared/sockets/adapters"
)

type SocketUpgrader interface {
	io.Closer

	Upgrade(w http.ResponseWriter, r *http.Request) (adapters.SocketAdapter, error)
	ListenAndServe() error
	Matches(r *http.Request) bool
}
