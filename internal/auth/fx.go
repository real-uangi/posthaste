package auth

import (
	"github.com/real-uangi/posthaste/internal/auth/application"
	"github.com/real-uangi/posthaste/internal/auth/infra"
	"go.uber.org/fx"
)

var Module = fx.Module("auth", fx.Provide(infra.NewVerifier, infra.NewSessions, application.NewService))
