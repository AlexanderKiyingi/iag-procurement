package iam

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is embedded in the access JWT.
type Claims struct {
	UserID      int64    `json:"uid"`
	Email       string   `json:"email"`
	IsSuperuser bool     `json:"super"`
	Permissions []string `json:"perms"`
	jwt.RegisteredClaims
}

func SignAccessToken(secret []byte, c Claims, ttl time.Duration) (string, error) {
	now := time.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		Subject:   fmt.Sprint(c.UserID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(secret)
}

func ParseAccessToken(secret []byte, token string) (*Claims, error) {
	var c Claims
	t, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !t.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return &c, nil
}
