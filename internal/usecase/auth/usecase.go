// Package auth implements registration, login, token refresh, and logout.
// It orchestrates the OrganizationRepository, UserRepository, RoleRepository
// and TeamMemberRepository behind their interfaces — nothing in this file
// imports gorm or database/sql.
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/platform/notify"
	"github.com/replypilot/backend/pkg/hash"
	"github.com/replypilot/backend/pkg/jwtutil"
	"github.com/replypilot/backend/pkg/otp"
)

// OTP purposes namespace codes issued by RequestRegistrationCode vs
// ForgotPassword — see internal/repository/redis.OTPStore's doc comment
// on why purpose+email keying matters (a code for one flow must not be
// redeemable against the other).
const (
	otpPurposeRegister      = "register"
	otpPurposeResetPassword = "password_reset"
)

// RefreshTokenStore allowlists issued refresh tokens so logout (or a future
// "revoke all sessions" action) can invalidate one before its natural
// expiry — a bare JWT can't be revoked, only allowed to expire. Implemented
// by internal/repository/redis.RefreshTokenStore.
type RefreshTokenStore interface {
	Store(ctx context.Context, jti string, userID uuid.UUID, ttl time.Duration) error
	Exists(ctx context.Context, jti string) (bool, error)
	Revoke(ctx context.Context, jti string) error
}

// OTPStore holds short-lived, purpose+email-keyed verification codes
// issued by RequestRegistrationCode and ForgotPassword, and redeemed by
// Register and ResetPassword respectively. Implemented by
// internal/repository/redis.OTPStore — see that type's doc comment for
// the attempt-limiting and single-use semantics.
type OTPStore interface {
	Save(ctx context.Context, purpose, email, code string) error
	Verify(ctx context.Context, purpose, email, code string) (bool, error)
}

type UseCase struct {
	orgRepo    repository.OrganizationRepository
	userRepo   repository.UserRepository
	roleRepo   repository.RoleRepository
	memberRepo repository.TeamMemberRepository
	tokens     *jwtutil.Manager
	refresh    RefreshTokenStore
	otpStore   OTPStore
	notifier   notify.EmailSender
}

func New(
	orgRepo repository.OrganizationRepository,
	userRepo repository.UserRepository,
	roleRepo repository.RoleRepository,
	memberRepo repository.TeamMemberRepository,
	tokens *jwtutil.Manager,
	refresh RefreshTokenStore,
	otpStore OTPStore,
	notifier notify.EmailSender,
) *UseCase {
	return &UseCase{
		orgRepo:    orgRepo,
		userRepo:   userRepo,
		roleRepo:   roleRepo,
		memberRepo: memberRepo,
		tokens:     tokens,
		refresh:    refresh,
		otpStore:   otpStore,
		notifier:   notifier,
	}
}

type RegisterInput struct {
	OrganizationName string
	OrganizationSlug string
	FullName         string
	Email            string
	Password         string
	// Code is the 6-digit code issued by RequestRegistrationCode to
	// in.Email — required so registration only succeeds for an email the
	// registrant actually controls (see package doc note in
	// RequestRegistrationCode).
	Code string
}

type LoginInput struct {
	Email          string
	Password       string
	OrganizationID uuid.UUID
}

type Result struct {
	User         *entity.User
	Organization *entity.Organization
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// RequestRegistrationCode sends a 6-digit verification code to email,
// which Register then requires back as RegisterInput.Code. This is what
// stops someone registering with a throwaway/mistyped/someone-else's
// address — Register cannot succeed without proving control of the inbox
// first (see user's explicit requirement: "userlar har xil emaildan
// register qilib tashlamasdan haqiqiy emaillardan ro'yxatadn o'tishi
// kerak").
//
// Deliberately does NOT reveal whether the email is already registered
// via a different error — it returns the existing-account conflict
// as-is, same as Register would, so the registration form's first
// interaction is where that's surfaced, not silently different behavior
// here that would need its own enumeration analysis.
func (uc *UseCase) RequestRegistrationCode(ctx context.Context, email string) error {
	if _, err := uc.userRepo.FindByEmail(ctx, email); err == nil {
		return apperror.Conflict("an account with this email already exists")
	} else if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return err
	}

	code, err := otp.Generate()
	if err != nil {
		return apperror.Internal("generate registration code", err)
	}
	if err := uc.otpStore.Save(ctx, otpPurposeRegister, email, code); err != nil {
		return apperror.Internal("store registration code", err)
	}

	subject := "Your verification code"
	html := fmt.Sprintf(
		"<p>Your verification code is:</p><p style=\"font-size:24px;font-weight:bold;letter-spacing:4px\">%s</p><p>This code expires in 10 minutes. If you didn't request this, you can ignore this email.</p>",
		code,
	)
	if err := uc.notifier.Send(ctx, email, subject, html); err != nil {
		return apperror.Internal("send registration code", err)
	}
	return nil
}

// Register provisions a brand-new organization and its first user as
// Owner — the only path that creates an org with no invite. Every
// subsequent member joins through a TeamMember invite; invite-acceptance
// isn't implemented in this skeleton (see README for the extension
// pattern), but TeamMemberRepository already supports it.
func (uc *UseCase) Register(ctx context.Context, in RegisterInput) (*Result, error) {
	if _, err := uc.userRepo.FindByEmail(ctx, in.Email); err == nil {
		return nil, apperror.Conflict("an account with this email already exists")
	} else if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return nil, err
	}

	if _, err := uc.orgRepo.FindBySlug(ctx, in.OrganizationSlug); err == nil {
		return nil, apperror.Conflict("organization slug already taken")
	} else if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return nil, err
	}

	verified, err := uc.otpStore.Verify(ctx, otpPurposeRegister, in.Email, in.Code)
	if err != nil {
		return nil, apperror.Internal("verify registration code", err)
	}
	if !verified {
		return nil, apperror.Unauthorized("invalid or expired verification code")
	}

	passwordHash, err := hash.Password(in.Password)
	if err != nil {
		return nil, apperror.Internal("hash password", err)
	}

	org := &entity.Organization{
		Name:   in.OrganizationName,
		Slug:   in.OrganizationSlug,
		Status: entity.OrganizationStatusTrial,
		// Default timezone: this codebase's primary market is Uzbekistan,
		// not a UTC-neutral default — most orgs will actually want this,
		// not need to change it. Still editable per-org via Settings.
		Timezone: "Asia/Tashkent",
	}
	if err := uc.orgRepo.Create(ctx, org); err != nil {
		return nil, err
	}

	user := &entity.User{
		Email:        in.Email,
		PasswordHash: &passwordHash,
		FullName:     in.FullName,
		Status:       entity.UserStatusActive,
	}
	if err := uc.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}

	ownerRole, err := uc.roleRepo.FindSystemRoleByName(ctx, entity.SystemRoleOwner)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	member := &entity.TeamMember{
		OrganizationID: org.ID,
		UserID:         user.ID,
		RoleID:         ownerRole.ID,
		Status:         entity.TeamMemberStatusActive,
		InvitedAt:      now,
		JoinedAt:       &now,
	}
	if err := uc.memberRepo.Create(ctx, member); err != nil {
		return nil, err
	}

	return uc.issueTokens(ctx, user, org, ownerRole.ID)
}

// Login authenticates against a specific organization, not just an email +
// password — a user who belongs to multiple orgs picks one up front (via
// org slug/subdomain at the delivery layer), rather than the API guessing.
func (uc *UseCase) Login(ctx context.Context, in LoginInput) (*Result, error) {
	user, err := uc.userRepo.FindByEmail(ctx, in.Email)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, apperror.Unauthorized("invalid email or password")
		}
		return nil, err
	}

	if user.PasswordHash == nil || !hash.ComparePassword(*user.PasswordHash, in.Password) {
		return nil, apperror.Unauthorized("invalid email or password")
	}

	if user.Status != entity.UserStatusActive {
		return nil, apperror.Forbidden("account is not active")
	}

	member, err := uc.memberRepo.FindByOrganizationAndUser(ctx, in.OrganizationID, user.ID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, apperror.Forbidden("you are not a member of this organization")
		}
		return nil, err
	}
	if member.Status != entity.TeamMemberStatusActive {
		return nil, apperror.Forbidden("your access to this organization is not active")
	}

	org, err := uc.orgRepo.FindByID(ctx, in.OrganizationID)
	if err != nil {
		return nil, err
	}
	if org.Status == entity.OrganizationStatusSuspended || org.Status == entity.OrganizationStatusCancelled {
		return nil, apperror.Forbidden("this organization's access has been suspended")
	}

	now := time.Now()
	user.LastLoginAt = &now
	if err := uc.userRepo.Update(ctx, user); err != nil {
		return nil, err
	}

	return uc.issueTokens(ctx, user, org, member.RoleID)
}

// Refresh exchanges a valid, non-revoked refresh token for a new access
// token. It does not rotate the refresh token itself — rotating on every
// use (revoking the old JTI, issuing a new one) is the stronger pattern for
// production and is a direct extension of the same RefreshTokenStore.
func (uc *UseCase) Refresh(ctx context.Context, refreshToken string) (*Result, error) {
	claims, err := uc.tokens.Parse(refreshToken, jwtutil.RefreshToken)
	if err != nil {
		return nil, apperror.Unauthorized("invalid or expired refresh token")
	}

	valid, err := uc.refresh.Exists(ctx, claims.ID)
	if err != nil {
		return nil, apperror.Internal("check refresh token store", err)
	}
	if !valid {
		return nil, apperror.Unauthorized("refresh token has been revoked")
	}

	user, err := uc.userRepo.FindByID(ctx, claims.UserID)
	if err != nil {
		return nil, apperror.Unauthorized("user no longer exists")
	}

	org, err := uc.orgRepo.FindByID(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	// Re-checked on every refresh, not just at login: a token issued
	// before an org was suspended should stop renewing well before its
	// full refresh-token TTL (168h default) — see
	// usecase/admin.UseCase.SuspendOrganization's doc comment on why this
	// still isn't mid-session-instant (the access token already in the
	// caller's hand keeps working until IT expires).
	if org.Status == entity.OrganizationStatusSuspended || org.Status == entity.OrganizationStatusCancelled {
		return nil, apperror.Forbidden("this organization's access has been suspended")
	}

	access, err := uc.tokens.GenerateAccessToken(claims.UserID, claims.OrganizationID, claims.RoleID, user.IsPlatformAdmin)
	if err != nil {
		return nil, apperror.Internal("generate access token", err)
	}

	return &Result{
		User:         user,
		Organization: org,
		AccessToken:  access.Token,
		RefreshToken: refreshToken,
		ExpiresAt:    access.ExpiresAt,
	}, nil
}

func (uc *UseCase) Logout(ctx context.Context, refreshToken string) error {
	claims, err := uc.tokens.Parse(refreshToken, jwtutil.RefreshToken)
	if err != nil {
		// Already invalid/expired: logging out of a token that can't be
		// used anyway is a success from the caller's point of view.
		return nil
	}
	return uc.refresh.Revoke(ctx, claims.ID)
}

// OrganizationMembership pairs an Organization with the caller's role
// within it — the shape the login screen needs to render "log into which
// workspace", not just a bare org list.
type OrganizationMembership struct {
	Organization *entity.Organization
	MemberStatus entity.TeamMemberStatus
}

// ListOrganizationsByEmail answers "which organizations can this email log
// into" — the step the frontend needs BEFORE it can call Login, since
// Login requires an organization_id and a user may belong to several
// (or zero) organizations.
//
// Unlike ForgotPassword, this intentionally DOES reveal "no account with
// that email" (via apperror.NotFound) rather than staying silent: this is
// a pre-login discovery step, not a password-recovery flow, and telling a
// user "we don't recognize that email, did you mean to register?" is
// normal, expected login UX (Gmail, GitHub, etc. all do this) — the
// enumeration concern that justifies silence in ForgotPassword doesn't
// carry the same weight here.
//
// Only active memberships are returned — an invited-but-not-joined or
// removed membership isn't a workspace the user can log into today.
func (uc *UseCase) ListOrganizationsByEmail(ctx context.Context, email string) ([]OrganizationMembership, error) {
	user, err := uc.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, apperror.NotFound("no account found with that email")
		}
		return nil, err
	}

	members, err := uc.memberRepo.ListByUserID(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	memberships := make([]OrganizationMembership, 0, len(members))
	for _, m := range members {
		if m.Status != entity.TeamMemberStatusActive {
			continue
		}
		org, err := uc.orgRepo.FindByID(ctx, m.OrganizationID)
		if err != nil {
			// A dangling membership (org deleted without cleaning up the
			// membership row) shouldn't fail the whole request — skip it.
			if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
				continue
			}
			return nil, err
		}
		memberships = append(memberships, OrganizationMembership{Organization: org, MemberStatus: m.Status})
	}

	return memberships, nil
}

// ForgotPassword issues a 6-digit verification code and hands it to the
// configured notify.EmailSender. It never returns an error for "no such
// email" — the caller (handler) always responds with the same generic "if
// that email exists, we sent a code" message regardless of whether the
// account exists, so an attacker can't use this endpoint to discover
// which emails are registered (the enumeration concern that
// ListOrganizationsByEmail deliberately does NOT worry about — different
// endpoints, different threat models).
func (uc *UseCase) ForgotPassword(ctx context.Context, email string) error {
	user, err := uc.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil // deliberately silent — see doc comment above
		}
		return err
	}
	if user.Status != entity.UserStatusActive {
		return nil // same reasoning: don't reveal account state to the caller
	}

	code, err := otp.Generate()
	if err != nil {
		return apperror.Internal("generate password reset code", err)
	}
	if err := uc.otpStore.Save(ctx, otpPurposeResetPassword, user.Email, code); err != nil {
		return apperror.Internal("store password reset code", err)
	}

	subject := "Your password reset code"
	html := fmt.Sprintf(
		"<p>Your password reset code is:</p><p style=\"font-size:24px;font-weight:bold;letter-spacing:4px\">%s</p><p>This code expires in 10 minutes. If you didn't request this, you can ignore this email.</p>",
		code,
	)
	if err := uc.notifier.Send(ctx, user.Email, subject, html); err != nil {
		return apperror.Internal("send password reset notification", err)
	}
	return nil
}

// ResetPassword redeems a code (issued by ForgotPassword to email) and
// sets a new password. The code is consumed on successful match, and
// discarded outright after too many wrong guesses — see
// internal/repository/redis.OTPStore.Verify's doc comment for the
// attempt-limiting behavior.
//
// Known scope limit, documented rather than silently skipped: this does
// NOT revoke the user's other active refresh tokens/sessions. Doing that
// requires tracking a user_id -> [jti...] set in
// internal/repository/redis.RefreshTokenStore (today it only stores
// individual JTIs, not indexed by user), which is a real follow-up but
// wasn't in scope here. Until then, a stolen-and-then-reset account can
// still be accessed via a refresh token issued before the reset.
func (uc *UseCase) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	verified, err := uc.otpStore.Verify(ctx, otpPurposeResetPassword, email, code)
	if err != nil {
		return apperror.Internal("verify password reset code", err)
	}
	if !verified {
		return apperror.Unauthorized("invalid or expired code")
	}

	user, err := uc.userRepo.FindByEmail(ctx, email)
	if err != nil {
		return apperror.Unauthorized("invalid or expired code")
	}

	passwordHash, err := hash.Password(newPassword)
	if err != nil {
		return apperror.Internal("hash password", err)
	}
	user.PasswordHash = &passwordHash

	if err := uc.userRepo.Update(ctx, user); err != nil {
		return err
	}
	return nil
}

func (uc *UseCase) issueTokens(ctx context.Context, user *entity.User, org *entity.Organization, roleID uuid.UUID) (*Result, error) {
	access, err := uc.tokens.GenerateAccessToken(user.ID, org.ID, roleID, user.IsPlatformAdmin)
	if err != nil {
		return nil, apperror.Internal("generate access token", err)
	}

	refreshToken, err := uc.tokens.GenerateRefreshToken(user.ID, org.ID, roleID, user.IsPlatformAdmin)
	if err != nil {
		return nil, apperror.Internal("generate refresh token", err)
	}

	if err := uc.refresh.Store(ctx, refreshToken.JTI, user.ID, uc.tokens.RefreshTokenTTL()); err != nil {
		return nil, apperror.Internal("store refresh token", err)
	}

	return &Result{
		User:         user,
		Organization: org,
		AccessToken:  access.Token,
		RefreshToken: refreshToken.Token,
		ExpiresAt:    access.ExpiresAt,
	}, nil
}
