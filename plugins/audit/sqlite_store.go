package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/glebarez/go-sqlite"
	"github.com/mywio/git-ops/pkg/core"
)

type sqliteStore struct {
	db *sql.DB
}

func newSQLiteStore(dbPath string) (*sqliteStore, error) {
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	store := &sqliteStore{db: db}
	if err := store.initSchema(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *sqliteStore) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS audit_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		source TEXT NOT NULL,
		repo TEXT,
		details TEXT,
		string_val TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_events(type);
	CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(timestamp);
	`
	_, err := s.db.Exec(query)
	return err
}

func (s *sqliteStore) Save(event core.InternalEvent) error {
	query := `
		INSERT INTO audit_events (type, timestamp, source, repo, details, string_val)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	var detailsStr sql.NullString
	if len(event.Details) > 0 {
		b, err := json.Marshal(event.Details)
		if err != nil {
			return err
		}
		detailsStr = sql.NullString{String: string(b), Valid: true}
	}

	_, err := s.db.Exec(query,
		string(event.Type),
		event.Timestamp,
		event.Source,
		event.Repo,
		detailsStr,
		event.String,
	)
	return err
}

func (s *sqliteStore) GetLastEvents(filter map[string]any, limit, offset int, order string) ([]core.InternalEvent, error) {
	query := "SELECT type, timestamp, source, repo, details, string_val FROM audit_events WHERE 1=1"
	var args []any

	if filter != nil {
		if t, ok := filter["type"].(string); ok && t != "" {
			query += " AND type = ?"
			args = append(args, t)
		}
		if src, ok := filter["source"].(string); ok && src != "" {
			query += " AND source = ?"
			args = append(args, src)
		}
		if repo, ok := filter["repo"].(string); ok && repo != "" {
			query += " AND repo = ?"
			args = append(args, repo)
		}
	}

	if strings.ToLower(order) == "asc" {
		query += " ORDER BY timestamp ASC"
	} else {
		query += " ORDER BY timestamp DESC"
	}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
		if offset > 0 {
			query += " OFFSET ?"
			args = append(args, offset)
		}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []core.InternalEvent
	for rows.Next() {
		var ev core.InternalEvent
		var typeStr, sourceStr string
		var detailsStr sql.NullString
		var stringVal sql.NullString
		var repoStr sql.NullString

		if err := rows.Scan(&typeStr, &ev.Timestamp, &sourceStr, &repoStr, &detailsStr, &stringVal); err != nil {
			return nil, err
		}

		ev.Type = core.EventTypeName(typeStr)
		ev.Source = sourceStr
		if repoStr.Valid {
			ev.Repo = repoStr.String
		}
		if stringVal.Valid {
			ev.String = stringVal.String
		}
		if detailsStr.Valid {
			if err := json.Unmarshal([]byte(detailsStr.String), &ev.Details); err != nil {
				return nil, err
			}
		}

		events = append(events, ev)
	}

	return events, rows.Err()
}

func (s *sqliteStore) Cleanup(keep int) error {
	if keep <= 0 {
		return nil
	}
	query := `
		DELETE FROM audit_events 
		WHERE id NOT IN (
			SELECT id FROM audit_events 
			ORDER BY timestamp DESC 
			LIMIT ?
		)
	`
	_, err := s.db.Exec(query, keep)
	return err
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
