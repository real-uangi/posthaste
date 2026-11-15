package infra

import (
	"github.com/real-uangi/eventbus"
	"github.com/real-uangi/posthaste/internal/notification/domain"
)

type Publisher struct {
	bus eventbus.EventBus
}

func NewPublisher(bus eventbus.EventBus) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) Publish(envelope domain.Envelope) error {
	if err := p.bus.Publish(domain.Topic, envelope); err != nil {
		return domain.ErrUnavailable
	}
	return nil
}
