// Package config loads and merges git-ops configuration from environment
// variables and YAML files.
//
// Config is the legacy typed core configuration used by existing plugins and
// runtime code. ConfigMap is the newer sectioned representation keyed by plugin
// name or "core", and is the preferred form for distributing configuration
// through PluginRegistry.
//
// LoadConfig and LoadConfigMapFromEnv read environment variables into the typed
// and sectioned forms respectively. LoadConfigFile reads YAML into a ConfigMap.
// MergeConfig and MergeConfigMap combine higher-priority values over fallbacks.
package config
