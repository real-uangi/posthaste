package eventadapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/real-uangi/eventbus"
	"github.com/real-uangi/posthaste/internal/notification/application"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/notification/infra"
)

type routeStore struct{ domain.Store }

func (routeStore) Routes(context.Context, string) ([]domain.Route, error) {
	return []domain.Route{{Caller: "default", Targets: []domain.TargetSnapshot{{ID: "destination", AccountID: "account", Platform: "test", Enabled: true}}}}, nil
}

type capturedDelivery struct {
	target  domain.TargetSnapshot
	message domain.Message
}

type captureSender struct{ delivered chan capturedDelivery }

func (*captureSender) Platform() string                      { return "test" }
func (*captureSender) ValidateAccount(json.RawMessage) error { return nil }
func (*captureSender) ValidateTarget(json.RawMessage) error  { return nil }
func (s *captureSender) Send(_ context.Context, target domain.TargetSnapshot, message domain.Message) error {
	s.delivered <- capturedDelivery{target: target, message: message}
	return nil
}

func TestAcceptedEnvelopeReachesFixedSubscriberAndDrains(t *testing.T) {
	bus := eventbus.New(&eventbus.Config{Dispatchers: 1, DispatcherQueueSize: 1, Executors: 1, ExecutorQueueSize: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer bus.Close(ctx)
	sender := &captureSender{delivered: make(chan capturedDelivery, 1)}
	service, err := application.NewService(routeStore{}, infra.NewPublisher(bus), []domain.Sender{sender}, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	subscribe(bus, service)
	accepted, err := service.Submit(context.Background(), "unknown", domain.Message{Message: "bridge test"})
	if err != nil || accepted.MessageID == "" {
		t.Fatalf("not accepted: %v", err)
	}
	if err := bus.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-sender.delivered:
		if delivered.message.Message != "bridge test" || delivered.message.Format != "text" || delivered.target.AccountID != "account" {
			t.Fatalf("unexpected delivery: %+v", delivered)
		}
	default:
		t.Fatal("accepted envelope was not delivered before close")
	}
	if _, err := service.Submit(context.Background(), "", domain.Message{Message: "after close"}); err != domain.ErrUnavailable {
		t.Fatalf("closed bus accepted a message: %v", err)
	}
}
