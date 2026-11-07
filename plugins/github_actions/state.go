package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

type actionsStore struct{ db *sql.DB }

type taskCredentialRecord struct {
	TaskID    string
	Spec      fleetapi.RunnerSpec
	Operation string
	UsedAt    *time.Time
}

func openActionsStore(path string) (*actionsStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	store := &actionsStore{db: db}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS github_deliveries (
 id TEXT PRIMARY KEY, event TEXT NOT NULL, received_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS runner_specs (
 id TEXT PRIMARY KEY, spec_json TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS runner_task_credentials (
 task_id TEXT PRIMARY KEY, spec_json TEXT NOT NULL, operation TEXT NOT NULL,
 created_at DATETIME NOT NULL, used_at DATETIME
);
`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *actionsStore) rememberDelivery(ctx context.Context, id, event string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO github_deliveries(id,event,received_at) VALUES(?,?,?)`, id, event, time.Now().UTC())
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM github_deliveries WHERE received_at<?`, time.Now().Add(-7*24*time.Hour))
	return count == 1, nil
}

func (s *actionsStore) saveRunner(ctx context.Context, spec fleetapi.RunnerSpec) error {
	data, _ := json.Marshal(spec)
	_, err := s.db.ExecContext(ctx, `INSERT INTO runner_specs(id,spec_json,created_at,updated_at) VALUES(?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET spec_json=excluded.spec_json,updated_at=excluded.updated_at`,
		spec.ID, string(data), time.Now().UTC(), time.Now().UTC())
	return err
}

func (s *actionsStore) runner(ctx context.Context, id string) (fleetapi.RunnerSpec, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT spec_json FROM runner_specs WHERE id=?`, id).Scan(&raw); err != nil {
		return fleetapi.RunnerSpec{}, err
	}
	var spec fleetapi.RunnerSpec
	return spec, json.Unmarshal([]byte(raw), &spec)
}

func (s *actionsStore) deleteRunner(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM runner_specs WHERE id=?`, id)
	return err
}

func (s *actionsStore) saveTaskCredential(ctx context.Context, taskID string, spec fleetapi.RunnerSpec, operation string) error {
	data, _ := json.Marshal(spec)
	_, err := s.db.ExecContext(ctx, `INSERT INTO runner_task_credentials(task_id,spec_json,operation,created_at) VALUES(?,?,?,?)`,
		taskID, string(data), operation, time.Now().UTC())
	return err
}

func (s *actionsStore) consumeTaskCredential(ctx context.Context, taskID string) (taskCredentialRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return taskCredentialRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var record taskCredentialRecord
	var raw string
	var used sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT spec_json,operation,used_at FROM runner_task_credentials WHERE task_id=?`, taskID).Scan(&raw, &record.Operation, &used); err != nil {
		return record, err
	}
	if used.Valid {
		return record, fmt.Errorf("runner credential was already redeemed")
	}
	if err := json.Unmarshal([]byte(raw), &record.Spec); err != nil {
		return record, err
	}
	now := time.Now().UTC()
	record.TaskID = taskID
	record.UsedAt = &now
	if _, err := tx.ExecContext(ctx, `UPDATE runner_task_credentials SET used_at=? WHERE task_id=? AND used_at IS NULL`, now, taskID); err != nil {
		return record, err
	}
	return record, tx.Commit()
}

func (s *actionsStore) close() error { return s.db.Close() }
