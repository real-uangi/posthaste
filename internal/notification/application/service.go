package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/real-uangi/allingo/common/log"
	"github.com/real-uangi/posthaste/internal/notification/domain"
)

type Service struct {
	store       domain.Store
	publisher   domain.Publisher
	senders     map[string]domain.Sender
	sendTimeout time.Duration
	logger      *log.StdLogger
}

func NewService(store domain.Store, publisher domain.Publisher, senders []domain.Sender, sendTimeout time.Duration) (*Service, error) {
	index := make(map[string]domain.Sender, len(senders))
	for _, sender := range senders {
		platform := sender.Platform()
		if _, exists := index[platform]; exists {
			return nil, errors.New("duplicate notification sender platform")
		}
		index[platform] = sender
	}
	return &Service{store: store, publisher: publisher, senders: index, sendTimeout: sendTimeout, logger: log.For[Service]()}, nil
}

func (s *Service) Submit(ctx context.Context, caller string, msg domain.Message) (domain.Acceptance, error) {
	msg, err := normalizeMessage(msg)
	if err != nil {
		return domain.Acceptance{}, err
	}
	routes, err := s.store.Routes(ctx, caller)
	if err != nil {
		return domain.Acceptance{}, err
	}
	var selected *domain.Route
	for i := range routes {
		if routes[i].Caller == caller && caller != "" {
			selected = &routes[i]
			break
		}
		if routes[i].Caller == "default" {
			selected = &routes[i]
		}
	}
	// A configured caller with no enabled members is an intentional empty route.
	// Falling back after this point would send to a group the caller did not select.
	if selected == nil || len(selected.Targets) == 0 {
		return domain.Acceptance{}, domain.ErrUnavailable
	}
	return s.publish(caller, selected.Caller, caller == "" || caller != selected.Caller, msg, selected.Targets)
}

func (s *Service) Test(ctx context.Context, id string, msg domain.Message) (domain.Acceptance, error) {
	if !validID(id) {
		return domain.Acceptance{}, domain.ErrInvalid
	}
	msg, err := normalizeMessage(msg)
	if err != nil {
		return domain.Acceptance{}, err
	}
	target, err := s.store.Target(ctx, id)
	if err != nil {
		return domain.Acceptance{}, err
	}
	if !target.Enabled {
		return domain.Acceptance{}, domain.ErrUnavailable
	}
	return s.publish("", "", false, msg, []domain.TargetSnapshot{target})
}

func (s *Service) publish(requested, resolved string, fallback bool, msg domain.Message, targets []domain.TargetSnapshot) (domain.Acceptance, error) {
	envelope := domain.Envelope{MessageID: uuid.NewString(), CallerRequested: requested, CallerResolved: resolved, Fallback: fallback, AcceptedAt: time.Now().UTC(), Message: msg, Targets: make([]domain.TargetSnapshot, len(targets))}
	for i, target := range targets {
		// The bus shares payload references. Copy mutable buffers so later config
		// writes or reuse by a store cannot change already accepted destinations.
		envelope.Targets[i] = target
		envelope.Targets[i].Config = append(json.RawMessage(nil), target.Config...)
		envelope.Targets[i].Credentials = append(json.RawMessage(nil), target.Credentials...)
	}
	if err := s.publisher.Publish(envelope); err != nil {
		return domain.Acceptance{}, domain.ErrUnavailable
	}
	s.logger.Infof("notification accepted messageId=%s callerRequested=%q callerResolved=%q fallback=%t", envelope.MessageID, requested, resolved, fallback)
	return domain.Acceptance{MessageID: envelope.MessageID}, nil
}

func (s *Service) Deliver(envelope domain.Envelope) error {
	var result error
	for _, target := range envelope.Targets {
		started := time.Now()
		sender, exists := s.senders[target.Platform]
		var err error
		if !exists {
			err = domain.ErrUnavailable
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), s.sendTimeout)
			err = sender.Send(ctx, target, envelope.Message)
			cancel()
		}
		if err != nil {
			// Sender errors are safe boundary errors, never raw URL/SDK errors.
			// Continue to independent targets even when an earlier destination fails.
			s.logger.Warnf("notification delivery messageId=%s accountId=%s targetId=%s platform=%s outcome=failed reason=%s elapsedMs=%d", envelope.MessageID, target.AccountID, target.ID, target.Platform, err.Error(), time.Since(started).Milliseconds())
			result = errors.Join(result, fmt.Errorf("target %s: %w", target.ID, err))
			continue
		}
		s.logger.Infof("notification delivery messageId=%s accountId=%s targetId=%s platform=%s outcome=succeeded elapsedMs=%d", envelope.MessageID, target.AccountID, target.ID, target.Platform, time.Since(started).Milliseconds())
	}
	return result
}

func (s *Service) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	return s.store.ListAccounts(ctx)
}

func (s *Service) SaveAccount(ctx context.Context, input domain.SaveAccount) (domain.Account, error) {
	if (input.ID != "" && !validID(input.ID)) || strings.TrimSpace(input.Name) == "" || (input.ID == "" && input.Credentials == nil) {
		return domain.Account{}, domain.ErrInvalid
	}
	sender, exists := s.senders[input.Platform]
	if !exists {
		return domain.Account{}, domain.ErrInvalid
	}
	if input.Credentials != nil {
		if err := sender.ValidateAccount(input.Credentials); err != nil {
			return domain.Account{}, domain.ErrInvalid
		}
	}
	return s.store.SaveAccount(ctx, input)
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	if !validID(id) {
		return domain.ErrInvalid
	}
	return s.store.DeleteAccount(ctx, id)
}

func (s *Service) ListTargets(ctx context.Context) ([]domain.Target, error) {
	return s.store.ListTargets(ctx)
}

func (s *Service) SaveTarget(ctx context.Context, input domain.SaveTarget) (domain.Target, error) {
	if (input.ID != "" && !validID(input.ID)) || !validID(input.AccountID) || strings.TrimSpace(input.Name) == "" {
		return domain.Target{}, domain.ErrInvalid
	}
	account, err := s.store.Account(ctx, input.AccountID)
	if err != nil {
		return domain.Target{}, err
	}
	// Account platforms are immutable. The transactional store rechecks that
	// the account exists; destination validation needs no credential reread.
	sender, exists := s.senders[account.Platform]
	if !exists {
		return domain.Target{}, domain.ErrInvalid
	}
	if err := sender.ValidateTarget(input.Config); err != nil {
		return domain.Target{}, domain.ErrInvalid
	}
	return s.store.SaveTarget(ctx, input)
}

func (s *Service) DeleteTarget(ctx context.Context, id string) error {
	if !validID(id) {
		return domain.ErrInvalid
	}
	return s.store.DeleteTarget(ctx, id)
}

func (s *Service) ListGroups(ctx context.Context) ([]domain.Group, error) {
	return s.store.ListGroups(ctx)
}

func (s *Service) SaveGroup(ctx context.Context, input domain.SaveGroup) (domain.Group, error) {
	if (input.ID != "" && !validID(input.ID)) || strings.TrimSpace(input.Name) == "" || input.Caller == "" || input.TargetIDs == nil {
		return domain.Group{}, domain.ErrInvalid
	}
	seen := make(map[string]struct{}, len(input.TargetIDs))
	ids := make([]string, 0, len(input.TargetIDs))
	for _, id := range input.TargetIDs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return domain.Group{}, domain.ErrInvalid
		}
		id = parsed.String()
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	input.TargetIDs = ids
	return s.store.SaveGroup(ctx, input)
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	if !validID(id) {
		return domain.ErrInvalid
	}
	return s.store.DeleteGroup(ctx, id)
}

func normalizeMessage(msg domain.Message) (domain.Message, error) {
	if strings.TrimSpace(msg.Message) == "" {
		return domain.Message{}, domain.ErrInvalid
	}
	if msg.Format == "" {
		msg.Format = "text"
	}
	if msg.Format != "text" && msg.Format != "markdown" {
		return domain.Message{}, domain.ErrInvalid
	}
	return msg, nil
}

func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}
