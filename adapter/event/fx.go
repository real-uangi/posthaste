package eventadapter

import (
	"errors"

	"github.com/real-uangi/eventbus"
	"github.com/real-uangi/posthaste/internal/notification/application"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"go.uber.org/fx"
)

var Module = fx.Module("event", fx.Invoke(subscribe))

func subscribe(bus eventbus.EventBus, service *application.Service) {
	bus.Subscribe(domain.Topic, func(data any) error {
		envelope, ok := data.(domain.Envelope)
		if !ok {
			return errors.New("invalid notification event")
		}
		return service.Deliver(envelope)
	})
}
