package middleware

import (
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware only lets same-origin browser requests through. The frontend
// is served from the same origin as the API (nginx proxies /api), so no other
// origin needs access; a cross-origin request is answered 403 before it
// reaches a handler, and credentials are never allowed cross-origin.
func CORSMiddleware() gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowOriginWithContextFunc: sameHostOrigin,
		AllowMethods:               []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:               []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key"},
		ExposeHeaders:              []string{"Content-Length"},
		MaxAge:                     12 * time.Hour,
	})
}

// sameHostOrigin accepts an Origin whose host name is the request's own host.
// gin-contrib/cors already passes an exact scheme://Host origin as a non-CORS
// request; this also covers a proxy that forwards Host without the port
// (nginx's $host) while the browser's Origin still carries it.
func sameHostOrigin(c *gin.Context, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host != "" && strings.EqualFold(u.Hostname(), host)
}
