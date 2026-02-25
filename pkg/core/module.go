package core

import (
	"context"
	"log/slog"
)

type Module interface {
	Name() string
	Init(ctx context.Context, logger *slog.Logger) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type ModuleManager struct {
	modules []Module
	logger  *slog.Logger
}

func NewModuleManager(logger *slog.Logger) *ModuleManager {
	return &ModuleManager{
		modules: []Module{},
		logger:  logger,
	}
}

func (m *ModuleManager) Register(mod Module) {
	m.modules = append(m.modules, mod)
}

func (m *ModuleManager) Init(ctx context.Context) error {
	for _, mod := range m.modules {
		if err := mod.Init(ctx, m.logger.With("module", mod.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (m *ModuleManager) Start(ctx context.Context) {
	for _, mod := range m.modules {
		go func(mod Module) {
			m.logger.Info("Starting module", "module", mod.Name())
			if err := mod.Start(ctx); err != nil {
				m.logger.Error("Module failed", "module", mod.Name(), "error", err)
			}
		}(mod)
	}
}

func (m *ModuleManager) Stop(ctx context.Context) {
	for i := len(m.modules) - 1; i >= 0; i-- {
		mod := m.modules[i]
		m.logger.Info("Stopping module", "module", mod.Name())
		if err := mod.Stop(ctx); err != nil {
			m.logger.Error("Error stopping module", "module", mod.Name(), "error", err)
		}
	}
}
