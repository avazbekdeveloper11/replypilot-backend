package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimiter implements a fixed-window counter in Redis. A fixed window is
// simpler than a sliding-window/token-bucket and precise enough for abuse
// protection; it is deliberately not a billing-accurate quota mechanism —
// that's usage_records (database/schema.sql), a wholly separate concern.
// Being Redis-backed (not in-process) is what makes this correct across N
// horizontally-scaled replicas of this service — an in-memory limiter would
// let a client get N times the intended rate by hitting different pods.
func RateLimiter(client *redis.Client, requestsPerMinute int, keyFunc func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		window := time.Now().Unix() / 60
		key := fmt.Sprintf("ratelimit:%s:%d", keyFunc(c), window)

		count, err := client.Incr(c.Request.Context(), key).Result()
		if err != nil {
			// Redis being unavailable should not take the whole API down.
			// Fail open rather than fail closed — an outage in the rate
			// limiter's own dependency is not a reason to reject every
			// request. This is a deliberate availability-over-strictness
			// choice; flag it if your threat model disagrees.
			c.Next()
			return
		}
		if count == 1 {
			client.Expire(c.Request.Context(), key, time.Minute)
		}

		if int(count) > requestsPerMinute {
			c.Header("Retry-After", "60")
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}

		c.Next()
	}
}

// ByIP keys the limiter by client IP — the right choice for unauthenticated
// routes (login, register, the Meta webhook) where no tenant context exists
// yet.
func ByIP(c *gin.Context) string {
	return c.ClientIP()
}

// ByOrganization keys the limiter by tenant instead of IP for authenticated
// routes: correct multi-tenant behavior is that one noisy tenant's traffic
// doesn't throttle a different tenant who happens to share a NAT gateway.
func ByOrganization(c *gin.Context) string {
	if v, ok := c.Get(CtxOrgID); ok {
		return fmt.Sprintf("%v", v)
	}
	return ByIP(c)
}
