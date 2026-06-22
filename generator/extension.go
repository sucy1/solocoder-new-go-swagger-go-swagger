// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"sync"

	"github.com/go-openapi/spec"
)

// SchemaExtensionHandler is a function that processes a schema extension.
// It receives the current schema and the extension value, and returns
// the modified schema or an error.
type SchemaExtensionHandler func(schema *spec.Schema, value interface{}) (*spec.Schema, error)

// extensionRegistry holds registered extension handlers.
type extensionRegistry struct {
	mu        sync.RWMutex
	handlers  map[string][]SchemaExtensionHandler
}

var defaultExtensionRegistry = &extensionRegistry{
	handlers: make(map[string][]SchemaExtensionHandler),
}

// RegisterExtensionHandler registers a handler for the given extension name.
// Handlers are executed in the order they are registered.
//
// The handler receives the schema and the extension value. It should return
// the modified schema. If the handler returns an error, processing stops
// and the error is propagated.
//
// Example:
//
//	generator.RegisterExtensionHandler("x-nullable", func(schema *spec.Schema, value interface{}) (*spec.Schema, error) {
//	    if nullable, ok := value.(bool); ok && nullable {
//	        schema.Nullable = true
//	    }
//	    return schema, nil
//	})
func RegisterExtensionHandler(extensionName string, handler SchemaExtensionHandler) {
	defaultExtensionRegistry.mu.Lock()
	defer defaultExtensionRegistry.mu.Unlock()
	defaultExtensionRegistry.handlers[extensionName] = append(
		defaultExtensionRegistry.handlers[extensionName],
		handler,
	)
}

// ClearExtensionHandlers removes all registered handlers for the given extension.
// If extensionName is empty, all handlers for all extensions are removed.
func ClearExtensionHandlers(extensionName string) {
	defaultExtensionRegistry.mu.Lock()
	defer defaultExtensionRegistry.mu.Unlock()

	if extensionName == "" {
		defaultExtensionRegistry.handlers = make(map[string][]SchemaExtensionHandler)
		return
	}

	delete(defaultExtensionRegistry.handlers, extensionName)
}

// applyExtensionHandlers applies all registered handlers for the given extension
// to the schema. Returns the modified schema.
func applyExtensionHandlers(extensionName string, schema *spec.Schema, value interface{}) (*spec.Schema, error) {
	defaultExtensionRegistry.mu.RLock()
	handlers := defaultExtensionRegistry.handlers[extensionName]
	defaultExtensionRegistry.mu.RUnlock()

	if len(handlers) == 0 {
		return schema, nil
	}

	var err error
	current := schema
	for _, handler := range handlers {
		current, err = handler(current, value)
		if err != nil {
			return current, err
		}
	}

	return current, nil
}

// applyAllExtensionHandlers applies all registered extension handlers to the schema.
// It iterates over all extensions in the schema and applies any registered handlers.
func applyAllExtensionHandlers(schema *spec.Schema) (*spec.Schema, error) {
	if schema == nil || schema.Extensions == nil {
		return schema, nil
	}

	current := schema
	var err error

	for extName, extValue := range schema.Extensions {
		current, err = applyExtensionHandlers(extName, current, extValue)
		if err != nil {
			return current, err
		}
	}

	return current, nil
}

// HasExtensionHandler checks if there is at least one handler registered
// for the given extension name.
func HasExtensionHandler(extensionName string) bool {
	defaultExtensionRegistry.mu.RLock()
	defer defaultExtensionRegistry.mu.RUnlock()
	_, ok := defaultExtensionRegistry.handlers[extensionName]
	return ok && len(defaultExtensionRegistry.handlers[extensionName]) > 0
}

// ListExtensionHandlers returns a list of all extension names that have
// registered handlers.
func ListExtensionHandlers() []string {
	defaultExtensionRegistry.mu.RLock()
	defer defaultExtensionRegistry.mu.RUnlock()

	names := make([]string, 0, len(defaultExtensionRegistry.handlers))
	for name, handlers := range defaultExtensionRegistry.handlers {
		if len(handlers) > 0 {
			names = append(names, name)
		}
	}
	return names
}
