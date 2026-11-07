package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

type fleetStore struct{ db *sql.DB }

type agentRecord struct {
	ID         string                  `json:"id"`
	Facts      fleetapi.AgentFacts     `json:"facts"`
	Runners    []fleetapi.RunnerStatus `json:"runners,omitempty"`
	LastSeen   time.Time               `json:"last_seen"`
	Online     bool                    `json:"online"`
	Revoked    bool                    `json:"revoked"`
	CertSerial string                  `json:"cert_serial,omitempty"`
}

func openFleetStore(path string) (*fleetStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	store := &fleetStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *fleetStore) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS agents (
 id TEXT PRIMARY KEY, node_name TEXT NOT NULL, pool TEXT NOT NULL,
 facts_json TEXT NOT NULL, runners_json TEXT NOT NULL DEFAULT '[]',
 last_seen DATETIME NOT NULL, cert_serial TEXT, revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS enrollment_tokens (
 token_hash TEXT PRIMARY KEY, pool TEXT NOT NULL, expires_at DATETIME NOT NULL,
 used_at DATETIME
);
CREATE TABLE IF NOT EXISTS agent_tasks (
 id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, type TEXT NOT NULL,
 generation INTEGER NOT NULL, payload_json TEXT NOT NULL,
 status TEXT NOT NULL, created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL,
 leased_at DATETIME, finished_at DATETIME, result_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_claim ON agent_tasks(agent_id,status,created_at);
`)
	return err
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *fleetStore) createEnrollment(ctx context.Context, token, pool string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO enrollment_tokens(token_hash,pool,expires_at) VALUES(?,?,?)`,
		tokenHash(token), pool, expires.UTC())
	return err
}

func (s *fleetStore) enroll(ctx context.Context, token string, agent agentRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var pool string
	var expires time.Time
	var used sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT pool,expires_at,used_at FROM enrollment_tokens WHERE token_hash=?`,
		tokenHash(token)).Scan(&pool, &expires, &used)
	if err != nil {
		return fmt.Errorf("invalid enrollment token")
	}
	if used.Valid || time.Now().After(expires) {
		return fmt.Errorf("enrollment token is expired or already used")
	}
	if pool != agent.Facts.Pool {
		return fmt.Errorf("enrollment token is scoped to pool %s", pool)
	}
	facts, _ := json.Marshal(agent.Facts)
	runners, _ := json.Marshal(agent.Runners)
	if _, err = tx.ExecContext(ctx, `INSERT INTO agents(id,node_name,pool,facts_json,runners_json,last_seen,cert_serial)
 VALUES(?,?,?,?,?,?,?)`, agent.ID, agent.Facts.NodeName, agent.Facts.Pool, string(facts), string(runners), agent.LastSeen, agent.CertSerial); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL`,
		time.Now().UTC(), tokenHash(token)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *fleetStore) heartbeat(ctx context.Context, id string, heartbeat fleetapi.HeartbeatRequest) error {
	facts, _ := json.Marshal(heartbeat.Facts)
	runners, _ := json.Marshal(heartbeat.Runners)
	result, err := s.db.ExecContext(ctx, `UPDATE agents SET node_name=?,pool=?,facts_json=?,runners_json=?,last_seen=?
 WHERE id=? AND revoked=0`, heartbeat.Facts.NodeName, heartbeat.Facts.Pool, string(facts), string(runners), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("agent is unknown or revoked")
	}
	return nil
}

func (s *fleetStore) listAgents(ctx context.Context, offlineAfter time.Duration) ([]agentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,facts_json,runners_json,last_seen,cert_serial,revoked FROM agents ORDER BY node_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []agentRecord
	for rows.Next() {
		var record agentRecord
		var facts, runners string
		if err := rows.Scan(&record.ID, &facts, &runners, &record.LastSeen, &record.CertSerial, &record.Revoked); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(facts), &record.Facts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(runners), &record.Runners); err != nil {
			return nil, err
		}
		record.Online = !record.Revoked && time.Since(record.LastSeen) <= offlineAfter
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *fleetStore) agentAllowed(ctx context.Context, id string) bool {
	var revoked bool
	err := s.db.QueryRowContext(ctx, `SELECT revoked FROM agents WHERE id=?`, id).Scan(&revoked)
	return err == nil && !revoked
}

func (s *fleetStore) revoke(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE agents SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *fleetStore) updateCert(ctx context.Context, id, serial string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET cert_serial=? WHERE id=? AND revoked=0`, serial, id)
	return err
}

func (s *fleetStore) queueTask(ctx context.Context, task fleetapi.Task, agentID string) error {
	payload, _ := json.Marshal(task.Payload)
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_tasks(id,agent_id,type,generation,payload_json,status,created_at,expires_at)
 VALUES(?,?,?,?,?,'queued',?,?)`, task.ID, agentID, task.Type, task.Generation, string(payload), time.Now().UTC(), task.ExpiresAt.UTC())
	return err
}

func (s *fleetStore) claimTask(ctx context.Context, agentID string, lease time.Duration) (*fleetapi.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.ExecContext(ctx, `UPDATE agent_tasks SET status='queued',leased_at=NULL WHERE agent_id=? AND status='leased' AND leased_at<?`, agentID, time.Now().Add(-lease))
	row := tx.QueryRowContext(ctx, `SELECT id,type,generation,payload_json,expires_at FROM agent_tasks
 WHERE agent_id=? AND status='queued' AND expires_at>? ORDER BY created_at LIMIT 1`, agentID, time.Now().UTC())
	var task fleetapi.Task
	var payload string
	if err := row.Scan(&task.ID, &task.Type, &task.Generation, &payload, &task.ExpiresAt); err != nil {
		if err == sql.ErrNoRows {
			_ = tx.Commit()
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(payload), &task.Payload); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET status='leased',leased_at=? WHERE id=? AND status='queued'`, time.Now().UTC(), task.ID)
	if err != nil {
		return nil, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return nil, fmt.Errorf("task lease conflict")
	}
	return &task, tx.Commit()
}

func (s *fleetStore) taskLeasedTo(ctx context.Context, taskID, agentID string) bool {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_tasks WHERE id=? AND agent_id=? AND status='leased'`, taskID, agentID).Scan(&count)
	return err == nil && count == 1
}

func (s *fleetStore) completeTask(ctx context.Context, agentID, taskID string, result fleetapi.TaskResult) error {
	body, _ := json.Marshal(result)
	status := result.Status
	if status != "succeeded" && status != "failed" {
		return fmt.Errorf("invalid task result status")
	}
	updated, err := s.db.ExecContext(ctx, `UPDATE agent_tasks SET status=?,finished_at=?,result_json=?
 WHERE id=? AND agent_id=? AND status='leased'`, status, time.Now().UTC(), string(body), taskID, agentID)
	if err != nil {
		return err
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return fmt.Errorf("task is not leased to this agent")
	}
	return nil
}

func (s *fleetStore) close() error { return s.db.Close() }
