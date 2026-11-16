package auth

import (
	uModel "donetick.com/core/internal/user/model"
	"github.com/gin-gonic/gin"
)

// CrewTAK: impersonating another user was removed (15 Nov 2026). Nothing sets
// the impersonated user any more, so the helpers below always return the
// signed-in user. They stay so existing callers keep compiling.

const (
	ImpersonatedUserKey = "impersonated_user"
	ActualUserKey       = "id"
)

// CurrentUserWithImpersonation returns both the actual user and impersonated user (if any)
// Returns (actualUser, impersonatedUser, hasImpersonation)
func CurrentUserWithImpersonation(c *gin.Context) (*uModel.UserDetails, *uModel.UserDetails, bool) {
	actualUser, ok := c.Get(ActualUserKey)
	if !ok {
		// No impersonation, return current user
		currentUser, exists := CurrentUser(c)
		return currentUser, nil, exists && false
	}

	impersonatedUser, impersonationExists := c.Get(ImpersonatedUserKey)
	if !impersonationExists {
		// Fallback to current user
		currentUser, exists := CurrentUser(c)
		return currentUser, nil, exists && false
	}

	actualUserDetails, actualOk := actualUser.(*uModel.UserDetails)
	impersonatedUserDetails, impersonatedOk := impersonatedUser.(*uModel.UserDetails)

	if !actualOk || !impersonatedOk {
		// Fallback to current user
		currentUser, exists := CurrentUser(c)
		return currentUser, nil, exists && false
	}

	return actualUserDetails, impersonatedUserDetails, true
}

// GetEffectiveUser returns the user that should be used for operations
// If impersonating, returns the impersonated user, otherwise returns the current user
func GetEffectiveUser(c *gin.Context) (*uModel.UserDetails, bool) {
	_, impersonatedUser, hasImpersonation := CurrentUserWithImpersonation(c)
	if hasImpersonation {
		return impersonatedUser, true
	}

	return CurrentUser(c)
}

// GetActualUser returns the actual authenticated user (ignoring impersonation)
func GetActualUser(c *gin.Context) (*uModel.UserDetails, bool) {
	actualUser, _, hasImpersonation := CurrentUserWithImpersonation(c)
	if hasImpersonation {
		return actualUser, true
	}

	return CurrentUser(c)
}
