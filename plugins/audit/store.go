package main

import "github.com/mywio/git-ops/pkg/core"

type AuditStore interface {
	Save(event core.InternalEvent) error
	GetLastEvents(filter map[string]any, limit, offset int, order string) ([]core.InternalEvent, error)
	Cleanup(keep int) error
	Close() error
}
