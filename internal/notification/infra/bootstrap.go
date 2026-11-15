package infra

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/real-uangi/allingo/common/db"
	"go.uber.org/fx"
	"gorm.io/gorm/clause"
)

// Bootstrap is an explicit dependency for the HTTP lifecycle. Its constructor
// declares hooks without migrating or querying the database.
type Bootstrap struct{}

func NewBootstrap(lc fx.Lifecycle, manager *db.Manager) (*Bootstrap, error) {
	database := manager.GetDB()
	sqlDB, err := database.DB()
	if err != nil {
		return nil, errors.New("database handle unavailable")
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) (startErr error) {
			// Fx only stops hooks whose OnStart completed. Release this handle
			// here when schema/config initialization prevents our hook starting.
			defer func() {
				if startErr != nil {
					_ = sqlDB.Close()
				}
			}()
			tx := database.WithContext(ctx)
			if err := tx.AutoMigrate(&accountRecord{}, &targetRecord{}, &groupRecord{}, &memberRecord{}); err != nil {
				return errors.New("notification schema initialization failed")
			}
			// Initial business configuration is separate from schema migration.
			// A unique caller makes this safe without overwriting existing members.
			group := groupRecord{ID: uuid.NewString(), Name: "默认目标组", Caller: "default"}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "caller"}}, DoNothing: true}).Create(&group).Error; err != nil {
				return errors.New("default notification group initialization failed")
			}
			return nil
		},
		OnStop: func(context.Context) error {
			if err := sqlDB.Close(); err != nil {
				return errors.New("database close failed")
			}
			return nil
		},
	})
	return &Bootstrap{}, nil
}
