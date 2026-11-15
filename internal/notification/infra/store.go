package infra

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/real-uangi/allingo/common/db"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type accountRecord struct {
	ID                    string `gorm:"type:uuid;primaryKey"`
	Name                  string `gorm:"not null"`
	Platform              string `gorm:"not null"`
	Enabled               bool   `gorm:"not null"`
	CredentialsCiphertext []byte `gorm:"type:bytea;not null"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (accountRecord) TableName() string { return "notification_accounts" }

type targetRecord struct {
	ID        string `gorm:"type:uuid;primaryKey"`
	AccountID string `gorm:"type:uuid;not null;index"`
	Name      string `gorm:"not null"`
	Enabled   bool   `gorm:"not null"`
	Config    string `gorm:"type:jsonb;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Account   accountRecord `gorm:"foreignKey:AccountID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (targetRecord) TableName() string { return "notification_targets" }

type groupRecord struct {
	ID        string `gorm:"type:uuid;primaryKey"`
	Name      string `gorm:"not null"`
	Caller    string `gorm:"not null;uniqueIndex"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (groupRecord) TableName() string { return "notification_groups" }

type memberRecord struct {
	GroupID  string       `gorm:"type:uuid;primaryKey"`
	TargetID string       `gorm:"type:uuid;primaryKey;index"`
	Group    groupRecord  `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Target   targetRecord `gorm:"foreignKey:TargetID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (memberRecord) TableName() string { return "notification_group_targets" }

type Store struct {
	db     *gorm.DB
	cipher *CredentialCipher
}

func NewStore(manager *db.Manager, cipher *CredentialCipher) *Store {
	return &Store{db: manager.GetDB(), cipher: cipher}
}

func (s *Store) Account(ctx context.Context, id string) (domain.Account, error) {
	var record accountRecord
	if err := s.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		return domain.Account{}, storageError(err)
	}
	return publicAccount(record), nil
}

func (s *Store) AccountSnapshot(ctx context.Context, id string) (domain.AccountSnapshot, error) {
	var record accountRecord
	if err := s.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		return domain.AccountSnapshot{}, storageError(err)
	}
	credentials, err := s.cipher.Decrypt(record.ID, record.CredentialsCiphertext)
	if err != nil {
		return domain.AccountSnapshot{}, domain.ErrUnavailable
	}
	return domain.AccountSnapshot{Account: publicAccount(record), Credentials: credentials}, nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	var records []accountRecord
	if err := s.db.WithContext(ctx).Order("created_at, id").Find(&records).Error; err != nil {
		return nil, storageError(err)
	}
	result := make([]domain.Account, 0, len(records))
	for _, record := range records {
		result = append(result, publicAccount(record))
	}
	return result, nil
}

func (s *Store) SaveAccount(ctx context.Context, input domain.SaveAccount) (domain.Account, error) {
	var result domain.Account
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record := accountRecord{ID: input.ID}
		if input.ID == "" {
			record.ID = uuid.NewString()
		} else {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", input.ID).Error; err != nil {
				return err
			}
			if record.Platform != input.Platform {
				return domain.ErrInvalid
			}
		}
		record.Name, record.Platform, record.Enabled = input.Name, input.Platform, input.Enabled
		if input.Credentials != nil {
			ciphertext, err := s.cipher.Encrypt(record.ID, input.Credentials)
			if err != nil {
				return err
			}
			record.CredentialsCiphertext = ciphertext
		}
		if input.ID == "" {
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else {
			// Map updates include explicit false. Omitted credentials retain the
			// locked row's ciphertext instead of clearing or re-encrypting it.
			updates := map[string]any{"name": record.Name, "platform": record.Platform, "enabled": record.Enabled}
			if input.Credentials != nil {
				updates["credentials_ciphertext"] = record.CredentialsCiphertext
			}
			if err := tx.Model(&record).Updates(updates).Error; err != nil {
				return err
			}
		}
		result = publicAccount(record)
		return nil
	})
	return result, storageError(err)
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	// A referenced account cannot be removed even when all its targets are
	// disabled. The declared FK also protects a concurrent target creation.
	result := s.db.WithContext(ctx).Delete(&accountRecord{}, "id = ?", id)
	if result.Error != nil {
		return storageError(result.Error)
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListTargets(ctx context.Context) ([]domain.Target, error) {
	var records []targetRecord
	if err := s.db.WithContext(ctx).Order("created_at, id").Find(&records).Error; err != nil {
		return nil, storageError(err)
	}
	result := make([]domain.Target, 0, len(records))
	for _, record := range records {
		result = append(result, publicTarget(record))
	}
	return result, nil
}

func (s *Store) SaveTarget(ctx context.Context, input domain.SaveTarget) (domain.Target, error) {
	var result domain.Target
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account accountRecord
		// The account row serializes Webhook target creation/rebinding. Counting
		// outside this lock would let two concurrent creates invent two exits for
		// a webhook that can only address its one fixed Feishu group.
		if err := tx.Select("id", "platform").Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, "id = ?", input.AccountID).Error; err != nil {
			return err
		}
		record := targetRecord{ID: input.ID}
		if input.ID == "" {
			record.ID = uuid.NewString()
		} else if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", input.ID).Error; err != nil {
			return err
		}
		if account.Platform == "feishu" {
			query := tx.Model(&targetRecord{}).Where("account_id = ?", account.ID)
			if input.ID != "" {
				query = query.Where("id <> ?", input.ID)
			}
			var count int64
			if err := query.Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return domain.ErrConflict
			}
		}
		record.AccountID, record.Name, record.Enabled, record.Config = account.ID, input.Name, input.Enabled, string(input.Config)
		if input.ID == "" {
			if err := tx.Omit(clause.Associations).Create(&record).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&record).Updates(map[string]any{"account_id": record.AccountID, "name": record.Name, "enabled": record.Enabled, "config": record.Config}).Error; err != nil {
				return err
			}
		}
		result = publicTarget(record)
		return nil
	})
	return result, storageError(err)
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	// RESTRICT on GroupTarget is the concurrency authority: a racing bind or
	// delete cannot pass a precheck and leave a dangling membership behind.
	result := s.db.WithContext(ctx).Delete(&targetRecord{}, "id = ?", id)
	if result.Error != nil {
		return storageError(result.Error)
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) Target(ctx context.Context, id string) (domain.TargetSnapshot, error) {
	var row dispatchRecord
	err := s.db.WithContext(ctx).Table("notification_targets AS t").
		Select("t.id AS target_id, t.account_id, t.name, t.config, (t.enabled AND a.enabled) AS enabled, a.platform, a.credentials_ciphertext").
		Joins("JOIN notification_accounts AS a ON a.id = t.account_id").
		Where("t.id = ?", id).Take(&row).Error
	if err != nil {
		return domain.TargetSnapshot{}, storageError(err)
	}
	return s.snapshot(row)
}

type dispatchRecord struct {
	Caller                string
	TargetID              *string
	AccountID             *string
	Name                  string
	Platform              string
	Enabled               bool
	Config                string
	CredentialsCiphertext []byte
}

func (s *Store) Routes(ctx context.Context, caller string) ([]domain.Route, error) {
	var rows []dispatchRecord
	// One SQL statement sees both candidate groups and all their enabled
	// destinations under the same PostgreSQL statement snapshot. LEFT JOIN
	// retains an empty configured group so the application can reject it.
	err := s.db.WithContext(ctx).Table("notification_groups AS g").
		Select("g.caller, t.id AS target_id, a.id AS account_id, t.name, a.platform, t.config, a.credentials_ciphertext").
		Joins("LEFT JOIN notification_group_targets AS m ON m.group_id = g.id").
		Joins("LEFT JOIN notification_targets AS t ON t.id = m.target_id AND t.enabled = ?", true).
		Joins("LEFT JOIN notification_accounts AS a ON a.id = t.account_id AND a.enabled = ?", true).
		Where("g.caller IN ?", []string{caller, "default"}).
		Order("g.caller, t.id").Scan(&rows).Error
	if err != nil {
		return nil, storageError(err)
	}
	routes := make([]domain.Route, 0, 2)
	indices := make(map[string]int, 2)
	for _, row := range rows {
		index, exists := indices[row.Caller]
		if !exists {
			index = len(routes)
			indices[row.Caller] = index
			routes = append(routes, domain.Route{Caller: row.Caller, Targets: []domain.TargetSnapshot{}})
		}
		// The group survives both LEFT JOINs even when all its accounts or exits
		// are disabled; it must remain an empty match rather than trigger fallback.
		if row.TargetID == nil || row.AccountID == nil {
			continue
		}
		row.Enabled = true
		snapshot, err := s.snapshot(row)
		if err != nil {
			return nil, err
		}
		routes[index].Targets = append(routes[index].Targets, snapshot)
	}
	return routes, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]domain.Group, error) {
	var rows []struct {
		ID       string
		Name     string
		Caller   string
		TargetID *string
	}
	err := s.db.WithContext(ctx).Table("notification_groups AS g").
		Select("g.id, g.name, g.caller, m.target_id").
		Joins("LEFT JOIN notification_group_targets AS m ON m.group_id = g.id").
		Order("g.created_at, g.id, m.target_id").Scan(&rows).Error
	if err != nil {
		return nil, storageError(err)
	}
	result := make([]domain.Group, 0)
	indices := make(map[string]int)
	for _, row := range rows {
		index, exists := indices[row.ID]
		if !exists {
			index = len(result)
			indices[row.ID] = index
			result = append(result, domain.Group{ID: row.ID, Name: row.Name, Caller: row.Caller, TargetIDs: []string{}})
		}
		if row.TargetID != nil {
			result[index].TargetIDs = append(result[index].TargetIDs, *row.TargetID)
		}
	}
	return result, nil
}

func (s *Store) SaveGroup(ctx context.Context, input domain.SaveGroup) (domain.Group, error) {
	var result domain.Group
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record := groupRecord{ID: input.ID}
		if input.ID == "" {
			record.ID = uuid.NewString()
		} else {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", input.ID).Error; err != nil {
				return err
			}
			if record.Caller == "default" && input.Caller != "default" {
				return domain.ErrConflict
			}
		}
		if len(input.TargetIDs) > 0 {
			var targets []targetRecord
			if err := tx.Select("id").Clauses(clause.Locking{Strength: "KEY SHARE"}).Where("id IN ?", input.TargetIDs).Find(&targets).Error; err != nil {
				return err
			}
			if len(targets) != len(input.TargetIDs) {
				return domain.ErrNotFound
			}
		}
		record.Name, record.Caller = input.Name, input.Caller
		if input.ID == "" {
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&record).Updates(map[string]any{"name": input.Name, "caller": input.Caller}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("group_id = ?", record.ID).Delete(&memberRecord{}).Error; err != nil {
			return err
		}
		members := make([]memberRecord, 0, len(input.TargetIDs))
		for _, targetID := range input.TargetIDs {
			members = append(members, memberRecord{GroupID: record.ID, TargetID: targetID})
		}
		if len(members) > 0 {
			if err := tx.Omit(clause.Associations).Create(&members).Error; err != nil {
				return err
			}
		}
		result = domain.Group{ID: record.ID, Name: record.Name, Caller: record.Caller, TargetIDs: append([]string{}, input.TargetIDs...)}
		return nil
	})
	return result, storageError(err)
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record groupRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error; err != nil {
			return err
		}
		if record.Caller == "default" {
			return domain.ErrConflict
		}
		if err := tx.Where("group_id = ?", id).Delete(&memberRecord{}).Error; err != nil {
			return err
		}
		return tx.Delete(&record).Error
	})
	return storageError(err)
}

func (s *Store) snapshot(record dispatchRecord) (domain.TargetSnapshot, error) {
	credentials, err := s.cipher.Decrypt(*record.AccountID, record.CredentialsCiphertext)
	if err != nil {
		return domain.TargetSnapshot{}, err
	}
	return domain.TargetSnapshot{ID: *record.TargetID, AccountID: *record.AccountID, Name: record.Name, Platform: record.Platform, Enabled: record.Enabled, Config: json.RawMessage(record.Config), Credentials: credentials}, nil
}

func publicTarget(record targetRecord) domain.Target {
	return domain.Target{ID: record.ID, AccountID: record.AccountID, Name: record.Name, Enabled: record.Enabled, Config: json.RawMessage(record.Config)}
}

func publicAccount(record accountRecord) domain.Account {
	return domain.Account{ID: record.ID, Name: record.Name, Platform: record.Platform, Enabled: record.Enabled, CredentialsConfigured: len(record.CredentialsCiphertext) > 0}
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{domain.ErrInvalid, domain.ErrNotFound, domain.ErrConflict, domain.ErrUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "23503", "23505":
			return domain.ErrConflict
		}
	}
	return domain.ErrUnavailable
}

var _ domain.Store = (*Store)(nil)
