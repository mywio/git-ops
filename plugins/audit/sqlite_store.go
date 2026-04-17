package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS audit_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		source TEXT NOT NULL,
		repo TEXT,
		execution_id TEXT,
		full_name TEXT,
		stage TEXT,
		status TEXT,
		last_error TEXT,
		failure_class TEXT,
		details TEXT,
		string_val TEXT
	);
	`
	if _, err := s.db.Exec(createTableQuery); err != nil {
		return err
	}

	if err := s.ensureExecutionColumns(); err != nil {
		return err
	}

	createIndexesQuery := `
	CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_events(type);
	CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_audit_execution_id ON audit_events(execution_id);
	CREATE INDEX IF NOT EXISTS idx_audit_full_name ON audit_events(full_name);
	CREATE INDEX IF NOT EXISTS idx_audit_stage ON audit_events(stage);
	CREATE INDEX IF NOT EXISTS idx_audit_status ON audit_events(status);
	CREATE INDEX IF NOT EXISTS idx_audit_failure_class ON audit_events(failure_class);
	`
	if _, err := s.db.Exec(createIndexesQuery); err != nil {
		return err
	}

	return s.backfillExecutionColumns()
}

func (s *sqliteStore) ensureExecutionColumns() error {
	existingColumns, err := s.auditEventColumns()
	if err != nil {
		return err
	}

	for _, column := range []struct {
		name string
		kind string
	}{
		{name: "execution_id", kind: "TEXT"},
		{name: "full_name", kind: "TEXT"},
		{name: "stage", kind: "TEXT"},
		{name: "status", kind: "TEXT"},
		{name: "last_error", kind: "TEXT"},
		{name: "failure_class", kind: "TEXT"},
	} {
		if _, ok := existingColumns[column.name]; ok {
			continue
		}

		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE audit_events ADD COLUMN %s %s", column.name, column.kind)); err != nil {
			return err
		}
	}

	return nil
}

func (s *sqliteStore) auditEventColumns() (map[string]struct{}, error) {
	rows, err := s.db.Query("PRAGMA table_info(audit_events)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var pk int

		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}

	return columns, rows.Err()
}

func (s *sqliteStore) backfillExecutionColumns() error {
	rows, err := s.db.Query(`
		SELECT id, details, execution_id, full_name, stage, status, last_error, failure_class
		FROM audit_events
		WHERE type = ?
		  AND details IS NOT NULL
		  AND (
			((execution_id IS NULL OR execution_id = '') AND COALESCE(json_extract(details, '$.execution_id'), '') != '') OR
			((full_name IS NULL OR full_name = '') AND COALESCE(json_extract(details, '$.full_name'), '') != '') OR
			((stage IS NULL OR stage = '') AND COALESCE(json_extract(details, '$.stage'), '') != '') OR
			((status IS NULL OR status = '') AND COALESCE(json_extract(details, '$.status'), '') != '') OR
			(last_error IS NULL AND json_type(details, '$.last_error') IS NOT NULL) OR
			(failure_class IS NULL AND json_type(details, '$.failure_class') IS NOT NULL)
		  )
	`, string(core.EventTypeExecution))
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyAuditRow struct {
		id           int64
		details      sql.NullString
		executionID  sql.NullString
		fullName     sql.NullString
		stage        sql.NullString
		status       sql.NullString
		lastError    sql.NullString
		failureClass sql.NullString
	}

	var legacyRows []legacyAuditRow
	for rows.Next() {
		var row legacyAuditRow
		if err := rows.Scan(
			&row.id,
			&row.details,
			&row.executionID,
			&row.fullName,
			&row.stage,
			&row.status,
			&row.lastError,
			&row.failureClass,
		); err != nil {
			return err
		}
		legacyRows = append(legacyRows, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, row := range legacyRows {
		if !row.details.Valid || row.details.String == "" {
			continue
		}

		var details map[string]any
		if err := json.Unmarshal([]byte(row.details.String), &details); err != nil {
			return err
		}

		event := core.InternalEvent{Details: details}
		fields := extractAuditExecutionFields(event)
		if _, err := s.db.Exec(`
			UPDATE audit_events
			SET execution_id = ?, full_name = ?, stage = ?, status = ?, last_error = ?, failure_class = ?
			WHERE id = ?
		`,
			preferString(row.executionID, fields.ExecutionID),
			preferString(row.fullName, fields.FullName),
			preferString(row.stage, fields.Stage),
			preferString(row.status, fields.Status),
			preferOptionalExecutionString(row.lastError, fields.LastError),
			preferOptionalExecutionString(row.failureClass, fields.FailureClass),
			row.id,
		); err != nil {
			return err
		}
	}

	return nil
}

func preferString(existing sql.NullString, fallback string) sql.NullString {
	if existing.Valid && existing.String != "" {
		return existing
	}
	if fallback == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: fallback, Valid: true}
}

func preferOptionalExecutionString(existing sql.NullString, fallback string) sql.NullString {
	if existing.Valid {
		return existing
	}

	return sql.NullString{String: fallback, Valid: true}
}

func (s *sqliteStore) Save(event core.InternalEvent) error {
	query := `
		INSERT INTO audit_events (
			type, timestamp, source, repo,
			execution_id, full_name, stage, status, last_error, failure_class,
			details, string_val
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var detailsStr sql.NullString
	if len(event.Details) > 0 {
		b, err := json.Marshal(event.Details)
		if err != nil {
			return err
		}
		detailsStr = sql.NullString{String: string(b), Valid: true}
	}

	fields := extractAuditExecutionFields(event)
	isExecutionEvent := event.Type == core.EventTypeExecution

	_, err := s.db.Exec(query,
		string(event.Type),
		event.Timestamp.UTC(),
		event.Source,
		event.Repo,
		sql.NullString{String: fields.ExecutionID, Valid: fields.ExecutionID != ""},
		sql.NullString{String: fields.FullName, Valid: fields.FullName != ""},
		sql.NullString{String: fields.Stage, Valid: fields.Stage != ""},
		sql.NullString{String: fields.Status, Valid: fields.Status != ""},
		sql.NullString{String: fields.LastError, Valid: isExecutionEvent || fields.LastError != ""},
		sql.NullString{String: fields.FailureClass, Valid: isExecutionEvent || fields.FailureClass != ""},
		detailsStr,
		event.Message,
	)
	return err
}

func (s *sqliteStore) GetLastEvents(filter map[string]any, limit, offset int, order string, since, until *time.Time) ([]core.InternalEvent, error) {
	query := "SELECT type, timestamp, source, repo, details, string_val FROM audit_events WHERE 1=1"
	var args []any

	if filter != nil {
		for _, key := range auditFilterKeys {
			value, ok := filter[key].(string)
			if !ok || value == "" {
				continue
			}

			column, ok := auditFilterColumns[key]
			if !ok {
				continue
			}

			query += fmt.Sprintf(" AND %s = ?", column)
			args = append(args, value)
		}
	}

	if since != nil {
		query += " AND timestamp >= ?"
		args = append(args, since.UTC())
	}
	if until != nil {
		query += " AND timestamp <= ?"
		args = append(args, until.UTC())
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
			ev.Message = stringVal.String
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
