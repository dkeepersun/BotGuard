package config

import (
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest"
)

// Connection limits applied by ServerLimits.
const (
	readHeaderTimeout = 5 * time.Second
	// readTimeout covers headers and body. Bodies are at most 32 KiB, so a
	// client still sending after this is trickling on purpose.
	readTimeout = 10 * time.Second
	// idleTimeout must stay above nginx's upstream keepalive_timeout (60s by
	// default): if the backend closes an idle connection first, nginx can
	// send the next request into the closing socket and answer 502.
	idleTimeout    = 120 * time.Second
	maxHeaderBytes = 64 << 10
	// writeMargin is added to the handler timeout for WriteTimeout, which
	// starts when the headers have been read.
	writeMargin = 10 * time.Second
)

// ServerLimits bounds how long a connection may take to send a request, to
// receive the response and to sit idle. go-zero's http.Server sets none of
// these, and its Timeout middleware only starts once the headers are in, so
// without them a client that trickles headers (slowloris) or a body holds a
// connection and a file descriptor indefinitely. handlerTimeout is the
// configured Timeout in milliseconds; zero (no handler timeout) leaves
// WriteTimeout unset.
func ServerLimits(handlerTimeout int64) rest.StartOption {
	return func(s *http.Server) {
		s.ReadHeaderTimeout = readHeaderTimeout
		s.ReadTimeout = readTimeout
		s.IdleTimeout = idleTimeout
		s.MaxHeaderBytes = maxHeaderBytes
		if handlerTimeout > 0 {
			s.WriteTimeout = time.Duration(handlerTimeout)*time.Millisecond + writeMargin
		}
	}
}
