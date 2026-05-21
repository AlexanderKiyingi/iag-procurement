package iam

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"iag-procurement/backend/internal/rbac"
)

type Service struct {
	store  *rbac.Store
	secret []byte
	ttl    time.Duration
}

func NewService(store *rbac.Store, secret []byte, ttl time.Duration) *Service {
	if ttl < time.Minute {
		ttl = 72 * time.Hour
	}
	return &Service{store: store, secret: secret, ttl: ttl}
}

type LoginResult struct {
	Token       string   `json:"token"`
	UserID      int64    `json:"userId"`
	Email       string   `json:"email"`
	IsSuperuser bool     `json:"isSuperuser"`
	Permissions []string `json:"permissions"`
}

func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.IsActive {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	perms, err := s.store.ListPermissionCodesForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	claims := Claims{
		UserID:      u.ID,
		Email:       u.Email,
		IsSuperuser: u.IsSuperuser,
		Permissions: perms,
	}
	tok, err := SignAccessToken(s.secret, claims, s.ttl)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:       tok,
		UserID:      u.ID,
		Email:       u.Email,
		IsSuperuser: u.IsSuperuser,
		Permissions: perms,
	}, nil
}

func (s *Service) ParseToken(token string) (*Claims, error) {
	return ParseAccessToken(s.secret, token)
}
