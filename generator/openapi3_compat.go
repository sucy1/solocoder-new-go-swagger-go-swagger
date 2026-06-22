// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-openapi/spec"
	"gopkg.in/yaml.v2"
)

const (
	openapi3SchemaRefPrefix  = "#/components/schemas/"
	swagger2DefRefPrefix     = "#/definitions/"
	openapi3ParamRefPrefix   = "#/components/parameters/"
	swagger2ParamRefPrefix   = "#/parameters/"
	openapi3RespRefPrefix    = "#/components/responses/"
	swagger2RespRefPrefix    = "#/responses/"
)

// OpenAPI3Converter converts OpenAPI 3.x specs to Swagger 2.0 compatible format.
//
// This converter focuses on $ref resolution compatibility:
// - Converts #/components/schemas/X to #/definitions/X
// - Converts #/components/parameters/X to #/parameters/X
// - Converts #/components/responses/X to #/responses/X
// - Moves components.schemas to definitions
// - Moves components.parameters to parameters
// - Moves components.responses to responses
type OpenAPI3Converter struct{}

// NewOpenAPI3Converter creates a new OpenAPI 3.x to Swagger 2.0 converter.
func NewOpenAPI3Converter() *OpenAPI3Converter {
	return &OpenAPI3Converter{}
}

// IsOpenAPI3 checks if the given raw spec data is an OpenAPI 3.x spec.
func IsOpenAPI3(data []byte) bool {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		// Try YAML
		if errYaml := yaml.Unmarshal(data, &m); errYaml != nil {
			return false
		}
	}

	if openapi, ok := m["openapi"]; ok {
		if version, ok := openapi.(string); ok {
			return strings.HasPrefix(version, "3.")
		}
	}

	return false
}

// GetOpenAPIVersion returns the OpenAPI version string, or empty string if not OpenAPI 3.x.
func GetOpenAPIVersion(data []byte) string {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		if errYaml := yaml.Unmarshal(data, &m); errYaml != nil {
			return ""
		}
	}

	if openapi, ok := m["openapi"]; ok {
		if version, ok := openapi.(string); ok {
			return version
		}
	}

	return ""
}

// ConvertOpenAPI3ToSwagger2 converts an OpenAPI 3.x spec to Swagger 2.0 format.
// It handles:
// - Converts OpenAPI 3.x $ref paths to Swagger 2.0 format
// - Moves components.schemas to definitions
// - Moves components.parameters to parameters
// - Moves components.responses to responses
// - Converts nullable to x-nullable extension for compatibility
func (c *OpenAPI3Converter) Convert(data []byte) ([]byte, error) {
	specMap := make(map[string]interface{})
	if err := json.Unmarshal(data, &specMap); err != nil {
		// Try YAML
		if errYaml := yaml.Unmarshal(data, &specMap); errYaml != nil {
			return nil, fmt.Errorf("failed to parse spec: %w", err)
		}
	}

	if err := c.convertMap(specMap); err != nil {
		return nil, err
	}

	// Remove openapi field, add swagger field
	delete(specMap, "openapi")
	specMap["swagger"] = "2.0"

	return json.MarshalIndent(specMap, "", "  ")
}

// convertMap recursively converts the spec map.
func (c *OpenAPI3Converter) convertMap(m map[string]interface{}) error {
	// Handle components -> definitions, parameters, responses
	if components, ok := m["components"]; ok {
		if componentsMap, ok := components.(map[string]interface{}); ok {
			// Move components.schemas -> definitions
			if schemas, ok := componentsMap["schemas"]; ok {
				m["definitions"] = schemas
			}
			// Move components.parameters -> parameters (top-level)
			if params, ok := componentsMap["parameters"]; ok {
				m["parameters"] = params
			}
			// Move components.responses -> responses (top-level)
			if responses, ok := componentsMap["responses"]; ok {
				m["responses"] = responses
			}
		}
		delete(m, "components")
	}

	// Convert $ref in paths
	if paths, ok := m["paths"]; ok {
		if pathsMap, ok := paths.(map[string]interface{}); ok {
			for pathName, pathItem := range pathsMap {
				if pathItemMap, ok := pathItem.(map[string]interface{}); ok {
					// Convert path-level parameters
					if params, ok := pathItemMap["parameters"]; ok {
						if paramsList, ok := params.([]interface{}); ok {
							for i, param := range paramsList {
								if paramMap, ok := param.(map[string]interface{}); ok {
									if err := c.convertRefsInMap(paramMap); err != nil {
										return fmt.Errorf("path %s parameter %d: %w", pathName, i, err)
									}
								}
							}
						}
					}

					// Convert operations
					for method, op := range pathItemMap {
						methodLower := strings.ToLower(method)
						if isHTTPMethod(methodLower) {
							if opMap, ok := op.(map[string]interface{}); ok {
								if err := c.convertRefsInMap(opMap); err != nil {
									return fmt.Errorf("path %s %s: %w", pathName, method, err)
								}
							}
						}
					}
				}
			}
		}
	}

	// Convert $ref in definitions (if any already exist or were just moved)
	if definitions, ok := m["definitions"]; ok {
		if defsMap, ok := definitions.(map[string]interface{}); ok {
			for defName, def := range defsMap {
				if defMap, ok := def.(map[string]interface{}); ok {
					if err := c.convertRefsInMap(defMap); err != nil {
						return fmt.Errorf("definition %s: %w", defName, err)
					}
				}
			}
		}
	}

	// Convert $ref in parameters (top-level)
	if parameters, ok := m["parameters"]; ok {
		if paramsMap, ok := parameters.(map[string]interface{}); ok {
			for paramName, param := range paramsMap {
				if paramMap, ok := param.(map[string]interface{}); ok {
					if err := c.convertRefsInMap(paramMap); err != nil {
						return fmt.Errorf("parameter %s: %w", paramName, err)
					}
				}
			}
		}
	}

	// Convert $ref in responses (top-level)
	if responses, ok := m["responses"]; ok {
		if respMap, ok := responses.(map[string]interface{}); ok {
			for respName, resp := range respMap {
				if rMap, ok := resp.(map[string]interface{}); ok {
					if err := c.convertRefsInMap(rMap); err != nil {
						return fmt.Errorf("response %s: %w", respName, err)
					}
				}
			}
		}
	}

	return nil
}

// convertRefsInMap converts all $ref values in a map from OpenAPI 3.x format to Swagger 2.0 format.
func (c *OpenAPI3Converter) convertRefsInMap(m map[string]interface{}) error {
	for key, value := range m {
		// Direct $ref
		if key == "$ref" {
			if refStr, ok := value.(string); ok {
				m[key] = convertRefPath(refStr)
			}
			continue
		}

		switch v := value.(type) {
		case map[string]interface{}:
			if err := c.convertRefsInMap(v); err != nil {
				return err
			}
			// Also handle nullable -> x-nullable conversion
			if nullable, ok := v["nullable"]; ok {
				if nullableBool, ok := nullable.(bool); ok && nullableBool {
					// Add x-nullable extension for Swagger 2.0 compatibility
					if exts, ok := v["extensions"].(map[string]interface{}); ok {
						exts["x-nullable"] = true
					} else {
						v["x-nullable"] = true
					}
				}
			}
		case []interface{}:
			for i, item := range v {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if err := c.convertRefsInMap(itemMap); err != nil {
						return err
					}
				}
				_ = i
			}
		}
	}

	return nil
}

// convertRefPath converts a single $ref path from OpenAPI 3.x to Swagger 2.0 format.
func convertRefPath(ref string) string {
	if strings.HasPrefix(ref, openapi3SchemaRefPrefix) {
		return swagger2DefRefPrefix + ref[len(openapi3SchemaRefPrefix):]
	}
	if strings.HasPrefix(ref, openapi3ParamRefPrefix) {
		return swagger2ParamRefPrefix + ref[len(openapi3ParamRefPrefix):]
	}
	if strings.HasPrefix(ref, openapi3RespRefPrefix) {
		return swagger2RespRefPrefix + ref[len(openapi3RespRefPrefix):]
	}
	return ref
}

// isHTTPMethod checks if the given string is an HTTP method name.
func isHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "delete", "patch", "head", "options":
		return true
	default:
		return false
	}
}

// ConvertOpenAPI3SchemaRefs converts $ref paths in a spec.Schema from OpenAPI 3.x to Swagger 2.0 format.
// This is useful for converting individual schemas, particularly for $ref resolution compatibility.
//
// It handles:
// - #/components/schemas/X -> #/definitions/X
// - #/components/parameters/X -> #/parameters/X
// - #/components/responses/X -> #/responses/X
//
// Also handles nullable property conversion:
// - schema.Nullable -> schema.Extensions["x-nullable"]
func ConvertOpenAPI3SchemaRefs(schema *spec.Schema) {
	if schema == nil {
		return
	}

	// Convert Ref
	if schema.Ref.String() != "" {
		refStr := schema.Ref.String()
		newRef := convertRefPath(refStr)
		if newRef != refStr {
			schema.Ref = spec.MustCreateRef(newRef)
		}
	}

	// Handle nullable conversion
	if schema.Nullable {
		if schema.Extensions == nil {
			schema.Extensions = make(spec.Extensions)
		}
		schema.Extensions[xNullable] = true
	}

	// Recursively convert items
	if schema.Items != nil && schema.Items.Schema != nil {
		ConvertOpenAPI3SchemaRefs(schema.Items.Schema)
	}
	if schema.Items != nil && len(schema.Items.Schemas) > 0 {
		for i := range schema.Items.Schemas {
			ConvertOpenAPI3SchemaRefs(&schema.Items.Schemas[i])
		}
	}

	// Recursively convert properties
	if schema.Properties != nil {
		for name := range schema.Properties {
			prop := schema.Properties[name]
			ConvertOpenAPI3SchemaRefs(&prop)
			schema.Properties[name] = prop
		}
	}

	// Recursively convert additionalProperties
	if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
		ConvertOpenAPI3SchemaRefs(schema.AdditionalProperties.Schema)
	}

	// Recursively convert allOf
	if len(schema.AllOf) > 0 {
		for i := range schema.AllOf {
			ConvertOpenAPI3SchemaRefs(&schema.AllOf[i])
		}
	}

	// Recursively convert anyOf
	if len(schema.AnyOf) > 0 {
		for i := range schema.AnyOf {
			ConvertOpenAPI3SchemaRefs(&schema.AnyOf[i])
		}
	}

	// Recursively convert oneOf
	if len(schema.OneOf) > 0 {
		for i := range schema.OneOf {
			ConvertOpenAPI3SchemaRefs(&schema.OneOf[i])
		}
	}

	// Recursively convert not
	if schema.Not != nil {
		ConvertOpenAPI3SchemaRefs(schema.Not)
	}
}
