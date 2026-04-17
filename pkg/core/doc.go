// Package core provides the runtime foundation for git-ops.
//
// The central type is ModuleManager, which owns plugin lifecycle, shared HTTP
// routing, configuration access, and the internal event bus. Plugins are loaded
// as Go plugins and initialized with a PluginRegistry so they can discover other
// plugins, register event types, subscribe to events, publish events, and access
// shared configuration.
//
// The primary lifecycle interfaces are Module and Plugin. Modules participate in
// Init, Start, and Stop. Plugins are modules that also expose metadata,
// capabilities, and an Execute entry point for plugin-specific actions.
//
// InternalEvent and related execution/event types define the shared event schema
// used across the reconciler, notifiers, UI, audit trail, and auxiliary system
// plugins.
package core
