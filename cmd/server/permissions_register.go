package main

import (
	"context"
	"log"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"

	"iag-procurement/backend/internal/config"
	"iag-procurement/backend/internal/models"
)

func registerPermissionsLoop(ctx context.Context, cfg *config.Config) {
	if cfg.ServiceClientSecret == "" {
		log.Printf("procurement: SERVICE_CLIENT_SECRET unset — skipping permissions registration")
		return
	}
	saClient := platformserviceauth.NewClient(platformserviceauth.Options{
		TokenURL:     cfg.AuthTokenURL,
		ClientID:     cfg.ServiceClientID,
		ClientSecret: cfg.ServiceClientSecret,
		Audience:     "iag.authentication",
	})
	descriptors := models.PermissionDescriptors()
	perms := make([]platformserviceauth.Permission, 0, len(descriptors))
	for _, d := range descriptors {
		perms = append(perms, platformserviceauth.Permission{
			Name:        d.Name,
			Description: d.Description,
		})
	}

	backoff := time.Second
	const maxBackoff = 5 * time.Minute
	for {
		regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := platformserviceauth.RegisterPermissions(regCtx, saClient, cfg.JWTIssuer, "procurement", perms)
		cancel()
		if err == nil {
			log.Printf("procurement: permissions registered with auth service (count=%d)", len(perms))
			return
		}
		log.Printf("procurement: permissions registration failed; retrying: %v (backoff=%s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
}
