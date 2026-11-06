package middleware

import (
	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/requestid"
)

// RequestID is the correlation middleware (S30/T33, refined S31/T35),
// registered FIRST (right after gin.Recovery): every later middleware,
// handler and logger runs inside a request context that carries the id.
//
// Trust boundary (S31): the client NEVER chooses the request id. The
// internal id is ALWAYS server-generated (requestid.NewID) — it is the
// response X-Request-ID header and the id every log record and problem
// instance carries. A sanitized incoming X-Request-ID is preserved
// SEPARATELY as client_request_id (own context key, own log attribute)
// purely for the client's cross-system correlation; an invalid or absent
// one is simply absent. Arbitrary attacker bytes are never echoed
// anywhere (requestid.Sanitize).
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := requestid.NewID()
		ctx := requestid.Put(c.Request.Context(), id)
		if clientID, ok := requestid.Sanitize(c.GetHeader("X-Request-ID")); ok {
			ctx = requestid.PutClient(ctx, clientID)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
