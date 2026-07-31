// Package user is the account-holder-facing counterpart to usecase/team
// (which manages OTHER people's membership in an org) — this one is "my
// own profile": read it, rename it, change my own password. Deliberately
// separate from usecase/auth (login/register/tokens) even though both
// touch UserRepository, since these are authenticated-self operations,
// not identity/session operations.
package user

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/hash"
)

type UseCase struct {
	userRepo repository.UserRepository
}

func New(userRepo repository.UserRepository) *UseCase {
	return &UseCase{userRepo: userRepo}
}

func (uc *UseCase) Me(ctx context.Context, userID uuid.UUID) (*entity.User, error) {
	return uc.userRepo.FindByID(ctx, userID)
}

// UpdateProfile updates the fields a user can change about themselves.
// Email is deliberately excluded — it's the login identifier and this
// codebase has no re-verification flow (confirm-new-email-address) for
// changing it, which is the standard reason self-serve email change is
// gated behind more than a plain PATCH. AvatarURL accepts any string the
// caller provides (e.g. a URL from an external upload service) — this
// codebase has no file upload/storage integration of its own.
func (uc *UseCase) UpdateProfile(ctx context.Context, userID uuid.UUID, fullName string, avatarURL *string) (*entity.User, error) {
	if fullName == "" {
		return nil, apperror.InvalidInput("full_name is required", nil)
	}
	u, err := uc.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	u.FullName = fullName
	u.AvatarURL = avatarURL
	if err := uc.userRepo.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword requires the current password — same defense as any
// "change my own password while logged in" flow: a hijacked, still-logged-in
// session shouldn't be able to lock the real owner out without knowing the
// current password. Contrast usecase/auth.UseCase.ResetPassword, the
// "I forgot my password" flow, which is a different trust model (a
// single-use emailed token stands in for "current password").
func (uc *UseCase) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	u, err := uc.userRepo.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.PasswordHash == nil || !hash.ComparePassword(*u.PasswordHash, currentPassword) {
		return apperror.Unauthorized("current password is incorrect")
	}

	newHash, err := hash.Password(newPassword)
	if err != nil {
		return apperror.Internal("hash password", err)
	}
	u.PasswordHash = &newHash
	return uc.userRepo.Update(ctx, u)
}
