package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, user *entity.User) error {
	model := userToModel(user)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return apperror.Internal("create user", err)
	}

	*user = *modelToUser(model)
	return nil
}

func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*entity.User, error) {
	var model UserModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("user not found")
		}
		return nil, apperror.Internal("find user by id", err)
	}
	return modelToUser(&model), nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*entity.User, error) {
	var model UserModel
	if err := r.db.WithContext(ctx).First(&model, "email = ?", email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("user not found")
		}
		return nil, apperror.Internal("find user by email", err)
	}
	return modelToUser(&model), nil
}

func (r *UserRepository) Update(ctx context.Context, user *entity.User) error {
	model := userToModel(user)
	res := r.db.WithContext(ctx).Model(&UserModel{}).Where("id = ?", user.ID).Updates(model)
	if res.Error != nil {
		return apperror.Internal("update user", res.Error)
	}
	if res.RowsAffected == 0 {
		return apperror.NotFound("user not found")
	}
	return nil
}

func userToModel(u *entity.User) *UserModel {
	return &UserModel{
		ID:              u.ID,
		Email:           u.Email,
		PasswordHash:    u.PasswordHash,
		FullName:        u.FullName,
		AvatarURL:       u.AvatarURL,
		Status:          string(u.Status),
		IsPlatformAdmin: u.IsPlatformAdmin,
		LastLoginAt:     u.LastLoginAt,
	}
}

func modelToUser(m *UserModel) *entity.User {
	e := &entity.User{
		ID:              m.ID,
		Email:           m.Email,
		PasswordHash:    m.PasswordHash,
		FullName:        m.FullName,
		AvatarURL:       m.AvatarURL,
		Status:          entity.UserStatus(m.Status),
		IsPlatformAdmin: m.IsPlatformAdmin,
		LastLoginAt:     m.LastLoginAt,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
