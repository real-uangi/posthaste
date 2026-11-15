package main

import (
	"github.com/gin-gonic/gin"
	"github.com/real-uangi/allingo/common"
	"github.com/real-uangi/allingo/common/app"
	"github.com/real-uangi/allingo/common/db"
	"github.com/real-uangi/allingo/common/log"
	"github.com/real-uangi/eventbus"
	eventadapter "github.com/real-uangi/posthaste/adapter/event"
	httpadapter "github.com/real-uangi/posthaste/adapter/http"
	"github.com/real-uangi/posthaste/internal/auth"
	"github.com/real-uangi/posthaste/internal/notification"
	"github.com/real-uangi/posthaste/internal/notification/infra"
	"github.com/real-uangi/posthaste/internal/shared"
	"github.com/real-uangi/posthaste/web"
	"go.uber.org/fx"
)

var appOptions = []fx.Option{
	fx.WithLogger(log.NewFxLogger),
	shared.Module, db.Module, notification.Module, auth.Module,
	fx.Provide(web.Assets),
	// Hook order follows dependencies: bootstrap/bus before HTTP on start,
	// HTTP before bus/database on stop. Module listing order alone is insufficient.
	fx.Decorate(func(engine *gin.Engine, _ eventbus.EventBus, _ *infra.Bootstrap) *gin.Engine { return engine }),
	eventadapter.Module, httpadapter.Module, common.Module,
}

func main() {
	for _, option := range appOptions {
		app.Current().Option(option)
	}
	app.Current().Run()
}
