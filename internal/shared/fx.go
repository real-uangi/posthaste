package shared

import (
	"context"

	"github.com/real-uangi/allingo/common/log"
	"github.com/real-uangi/eventbus"
	"github.com/real-uangi/posthaste/internal/shared/config"
	"go.uber.org/fx"
)

var Module = fx.Module("shared", fx.Provide(config.Load, newEventBus))

func newEventBus(lc fx.Lifecycle, cfg *config.Config) eventbus.EventBus {
	eventbus.WithLogger(log.For[eventbus.EventBus]())
	bus := eventbus.New(&eventbus.Config{
		Dispatchers: 1, DispatcherQueueSize: cfg.DispatcherQueueSize,
		Executors: cfg.Executors, ExecutorQueueSize: cfg.ExecutorQueueSize,
	})
	lc.Append(fx.Hook{OnStop: func(ctx context.Context) error { return bus.Close(ctx) }})
	return bus
}
