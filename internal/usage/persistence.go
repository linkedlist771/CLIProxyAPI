package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	log "github.com/sirupsen/logrus"
)

var persistenceMu sync.Mutex

// InitializePersistence opens the usage SQLite database beside the config file and loads stored rows.
func InitializePersistence(ctx context.Context, configFilePath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dbPath, err := SQLitePathForConfig(configFilePath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return fmt.Errorf("create usage data directory: %w", err)
	}
	store, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		return err
	}

	persistenceMu.Lock()
	defer persistenceMu.Unlock()
	if err = defaultRequestStatistics.ConfigureStore(ctx, store); err != nil {
		_ = store.Close()
		return err
	}
	log.Infof("usage statistics persistence enabled: %s", dbPath)
	return nil
}

// ClosePersistence closes the shared usage SQLite store.
func ClosePersistence() error {
	persistenceMu.Lock()
	defer persistenceMu.Unlock()
	return defaultRequestStatistics.CloseStore()
}

// SQLitePathForConfig returns the usage SQLite database path for a config file path.
func SQLitePathForConfig(configFilePath string) (string, error) {
	baseDir := "."
	if configFilePath != "" {
		baseDir = filepath.Dir(configFilePath)
	}
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve usage data directory: %w", err)
	}
	return filepath.Join(absBaseDir, "data", "usage.sqlite"), nil
}

func logStoreError(action string, err error) {
	if err != nil {
		log.WithError(err).Warn(action)
	}
}
