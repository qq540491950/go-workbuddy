package services

import (
	"os"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"
)

// openSessionServiceImpl mirrors main.go's newSessionService (which lives in
// package main and cannot be imported) so the self-heal path is testable.
func openSessionServiceImpl(path string) (*gorm.DB, session.Service, error) {
	open := func() (*gorm.DB, session.Service, error) {
		db, err := gorm.Open(sqlite.Open(path))
		if err != nil {
			return nil, nil, err
		}
		svc, err := database.NewSessionServiceFromDB(db)
		if err != nil {
			return nil, nil, err
		}
		if err := database.AutoMigrate(svc); err != nil {
			return nil, nil, err
		}
		// Probe: a corrupt file can pass Open but fail on first query.
		if err := db.Exec("SELECT count(*) FROM sessions").Error; err != nil {
			return nil, nil, err
		}
		return db, svc, nil
	}
	db, svc, err := open()
	if err != nil {
		backup := path + ".corrupt-test"
		_ = os.Rename(path, backup)
		db, svc, err = open()
		if err != nil {
			return nil, nil, err
		}
	}
	return db, svc, nil
}
