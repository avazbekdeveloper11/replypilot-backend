package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/pkg/jwtutil"
)

// Gin's Context is a map[string]any, not a typed context.Context — these
// string keys are the contract between Auth (which sets them) and every
// handler/helper that reads them (internal/delivery/http/v1/helpers.go).
const (
	CtxUserID          = "auth.user_id"
	CtxOrgID           = "auth.org_id"
	CtxRoleID          = "auth.role_id"
	CtxIsPlatformAdmin = "auth.is_platform_admin"
)

// Auth validates the Bearer access token on every protected route and, on
// success, puts the authenticated user/org/role into the Gin context.
// org_id from here is what every repository call downstream uses to scope
// its query and set the RLS session variable (internal/repository/postgres
// withTenant) — an invalid or missing token means no tenant context exists
// at all, not an empty one.
func Auth(tokens *jwtutil.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		tokenString := strings.TrimPrefix(header, "Bearer ")

		claims, err := tokens.Parse(tokenString, jwtutil.AccessToken)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxOrgID, claims.OrganizationID)
		c.Set(CtxRoleID, claims.RoleID)
		c.Set(CtxIsPlatformAdmin, claims.IsPlatformAdmin)
		c.Next()
	}
}

// RequirePlatformAdmin gates the /v1/admin/* route group. Must run after
// Auth (it reads what Auth put in the context) — every admin route
// registers both, Auth first, same ordering as RateLimitByOrg elsewhere.
// Rejects with 403, not 404: an unauthorized platform-admin request
// should read as "you can't do this," not "this doesn't exist," since
// unlike a tenant boundary there's no cross-tenant enumeration concern
// being protected by hiding the route's existence.
//
// Writes the standard JSON error envelope via response.Error (same shape
// every other error in this API uses) rather than a bare
// c.AbortWithStatus — every fetch helper in the frontend unconditionally
// parses the response body as JSON (see lib/api/client.ts's doc comment),
// so an empty body here surfaces client-side as a confusing "the API
// returned a non-JSON response" instead of a clear "you don't have
// platform admin access" message. This does NOT go through
// middleware.ErrorHandler (that only reacts to c.Error(), which requires
// c.Next() to have run) — it writes and aborts directly, same reasoning
// as why Auth can't use c.Error() either.
func RequirePlatformAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(CtxIsPlatformAdmin)
		isPlatformAdmin, typeOK := v.(bool)
		if !ok || !typeOK || !isPlatformAdmin {
			response.Error(c, apperror.Forbidden("platform admin access required"))
			c.Abort()
			return
		}
		c.Next()
	}
}
