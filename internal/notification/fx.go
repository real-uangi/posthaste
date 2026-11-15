package notification

import (
	"github.com/real-uangi/posthaste/internal/notification/application"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/notification/infra"
	"github.com/real-uangi/posthaste/internal/shared/config"
	"go.uber.org/fx"
)

var Module = fx.Module("notification",
	fx.Provide(
		infra.NewBootstrap,
		infra.NewCredentialCipher,
		fx.Annotate(infra.NewStore, fx.As(new(domain.Store))),
		fx.Annotate(infra.NewPublisher, fx.As(new(domain.Publisher))),
		fx.Annotate(infra.NewFeishuSender, fx.As(new(domain.Sender)), fx.ResultTags(`group:"notification_senders"`)),
		fx.Annotate(infra.NewTelegramSender, fx.As(new(domain.Sender)), fx.ResultTags(`group:"notification_senders"`)),
		fx.Annotate(infra.NewBarkSender, fx.As(new(domain.Sender)), fx.ResultTags(`group:"notification_senders"`)),
		infra.NewFeishuAppSender,
		fx.Annotate(func(sender *infra.FeishuAppSender) domain.Sender { return sender }, fx.ResultTags(`group:"notification_senders"`)),
		func(sender *infra.FeishuAppSender) domain.ChatLister { return sender },
		newService,
	),
)

type serviceParams struct {
	fx.In
	Store      domain.Store
	Publisher  domain.Publisher
	Config     *config.Config
	Senders    []domain.Sender `group:"notification_senders"`
	ChatLister domain.ChatLister
}

func newService(p serviceParams) (*application.Service, error) {
	return application.NewService(p.Store, p.Publisher, p.Senders, p.ChatLister, p.Config.SendTimeout)
}
