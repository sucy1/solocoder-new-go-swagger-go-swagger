// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
)

const (
	// Output messages.
	missingArgMsg     = "the validate command requires the swagger document url to be specified"
	validSpecMsg      = "\nThe swagger spec at %q is valid against swagger specification %s\n"
	invalidSpecMsg    = "\nThe swagger spec at %q is invalid against swagger specification %s.\nSee errors below:\n"
	warningSpecMsg    = "\nThe swagger spec at %q showed up some valid but possibly unwanted constructs."
	strictErrorsMsg   = "\nThe swagger spec at %q has strict validation errors.\nSee errors below:\n"
)

// ValidateSpec is a command that validates a swagger document
// against the swagger specification.
type ValidateSpec struct {
	// SchemaURL string `long:"schema" description:"The schema url to use" default:"http://swagger.io/v2/schema.json"`
	SkipWarnings bool `description:"when present will not show up warnings upon validation"                    long:"skip-warnings"`
	StopOnError  bool `description:"when present will not continue validation after critical errors are found" long:"stop-on-error"`
	Strict       bool `description:"enable strict validation: unique operationId, camelCase parameters, standard response codes" long:"strict"`
}

// Execute validates the spec.
func (c *ValidateSpec) Execute(args []string) error {
	if len(args) == 0 {
		return errors.New(missingArgMsg)
	}

	swaggerDoc := args[0]

	specDoc, err := loads.Spec(swaggerDoc)
	if err != nil {
		return err
	}

	// Attempts to report about all errors
	validate.SetContinueOnErrors(!c.StopOnError)

	v := validate.NewSpecValidator(specDoc.Schema(), strfmt.Default)
	result, _ := v.Validate(specDoc) // returns fully detailed result with errors and warnings

	if result.IsValid() {
		log.Printf(validSpecMsg, swaggerDoc, specDoc.Version())
	}
	if result.HasWarnings() {
		log.Printf(warningSpecMsg, swaggerDoc)
		if !c.SkipWarnings {
			log.Printf("See warnings below:\n")
			for _, desc := range result.Warnings {
				log.Printf("- WARNING: %s\n", desc.Error())
			}
		}
	}
	if result.HasErrors() {
		var buf strings.Builder
		fmt.Fprintf(&buf, invalidSpecMsg, swaggerDoc, specDoc.Version())
		for _, desc := range result.Errors {
			fmt.Fprintf(&buf, "- %s\n", desc.Error())
		}
		return errors.New(buf.String())
	}

	if c.Strict {
		if strictErrs := runStrictValidations(specDoc.Spec()); len(strictErrs) > 0 {
			var buf strings.Builder
			fmt.Fprintf(&buf, strictErrorsMsg, swaggerDoc)
			for _, e := range strictErrs {
				fmt.Fprintf(&buf, "- %s\n", e)
			}
			return errors.New(buf.String())
		}
		log.Printf("Strict validation passed for %q\n", swaggerDoc)
	}

	return nil
}

// runStrictValidations runs all strict validations and returns the errors.
func runStrictValidations(swspec *spec.Swagger) []string {
	var errs []string

	errs = append(errs, validateUniqueOperationIDs(swspec)...)
	errs = append(errs, validateCamelCaseParameters(swspec)...)
	errs = append(errs, validateStandardResponseCodes(swspec)...)
	errs = append(errs, validateRefExistence(swspec)...)

	return errs
}

// validateUniqueOperationIDs checks that all operationId values are unique.
func validateUniqueOperationIDs(swspec *spec.Swagger) []string {
	var errs []string
	operationIDs := make(map[string]string) // operationId -> method+path

	if swspec == nil || swspec.Paths == nil {
		return errs
	}

	for path, pathItem := range swspec.Paths.Paths {
		ops := map[string]*spec.Operation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"DELETE":  pathItem.Delete,
			"PATCH":   pathItem.Patch,
			"HEAD":    pathItem.Head,
			"OPTIONS": pathItem.Options,
		}

		for method, op := range ops {
			if op == nil || op.ID == "" {
				continue
			}
			location := fmt.Sprintf("%s %s", method, path)
			if prevLoc, exists := operationIDs[op.ID]; exists {
				errs = append(errs, fmt.Sprintf("duplicate operationId %q: found at %s and %s", op.ID, prevLoc, location))
			} else {
				operationIDs[op.ID] = location
			}
		}
	}

	return errs
}

// validateCamelCaseParameters checks that all parameter names follow camelCase convention.
func validateCamelCaseParameters(swspec *spec.Swagger) []string {
	var errs []string

	if swspec == nil || swspec.Paths == nil {
		return errs
	}

	for path, pathItem := range swspec.Paths.Paths {
		ops := map[string]*spec.Operation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"DELETE":  pathItem.Delete,
			"PATCH":   pathItem.Patch,
			"HEAD":    pathItem.Head,
			"OPTIONS": pathItem.Options,
		}

		for method, op := range ops {
			if op == nil {
				continue
			}
			for _, param := range op.Parameters {
				if param.Name == "" {
					continue
				}
				if !isCamelCase(param.Name) {
					errs = append(errs, fmt.Sprintf("parameter name %q at %s %s is not camelCase", param.Name, method, path))
				}
			}
		}

		// Check path-level parameters
		for _, param := range pathItem.Parameters {
			if param.Name == "" {
				continue
			}
			if !isCamelCase(param.Name) {
				errs = append(errs, fmt.Sprintf("path-level parameter name %q at %s is not camelCase", param.Name, path))
			}
		}
	}

	return errs
}

// isCamelCase checks if a string follows camelCase naming convention.
// camelCase: first letter lowercase, no underscores, no hyphens, no spaces.
func isCamelCase(s string) bool {
	if s == "" {
		return true
	}

	runes := []rune(s)

	// First character must be lowercase
	if !unicode.IsLower(runes[0]) {
		return false
	}

	// No underscores, hyphens, or spaces
	for _, r := range runes {
		if r == '_' || r == '-' || unicode.IsSpace(r) {
			return false
		}
	}

	return true
}

// validateStandardResponseCodes checks that all response status codes are within standard ranges.
// Standard ranges: 1xx (informational), 2xx (success), 3xx (redirection), 4xx (client error), 5xx (server error)
func validateStandardResponseCodes(swspec *spec.Swagger) []string {
	var errs []string

	if swspec == nil {
		return errs
	}

	// Check global responses
	for code := range swspec.Responses {
		if !isStandardResponseCode(code) {
			errs = append(errs, fmt.Sprintf("global response code %q is not a standard HTTP status code", code))
		}
	}

	if swspec.Paths == nil {
		return errs
	}

	for path, pathItem := range swspec.Paths.Paths {
		ops := map[string]*spec.Operation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"DELETE":  pathItem.Delete,
			"PATCH":   pathItem.Patch,
			"HEAD":    pathItem.Head,
			"OPTIONS": pathItem.Options,
		}

		for method, op := range ops {
			if op == nil || op.Responses == nil {
				continue
			}

			// Check default is valid
			if op.Responses.Default != nil {
				// "default" is always valid
			}

			for code := range op.Responses.StatusCodeResponses {
				codeStr := strconv.Itoa(code)
				if !isStandardResponseCode(codeStr) {
					errs = append(errs, fmt.Sprintf("response code %d at %s %s is not a standard HTTP status code", code, method, path))
				}
			}
		}
	}

	return errs
}

// isStandardResponseCode checks if a response code string represents a standard HTTP status code.
// Accepts numeric codes in 1xx-5xx ranges, as well as "default".
func isStandardResponseCode(code string) bool {
	if code == "default" {
		return true
	}

	codeInt, err := strconv.Atoi(code)
	if err != nil {
		return false
	}

	return codeInt >= 100 && codeInt < 600 && http.StatusText(codeInt) != ""
}

// validateRefExistence checks that all $ref references point to existing definitions.
// It validates references to definitions, parameters, and responses.
func validateRefExistence(swspec *spec.Swagger) []string {
	var errs []string

	if swspec == nil {
		return errs
	}

	// Collect available definitions
	availableDefinitions := make(map[string]bool)
	for name := range swspec.Definitions {
		availableDefinitions[name] = true
	}

	// Collect available parameters
	availableParams := make(map[string]bool)
	for name := range swspec.Parameters {
		availableParams[name] = true
	}

	// Collect available responses
	availableResponses := make(map[string]bool)
	for name := range swspec.Responses {
		availableResponses[name] = true
	}

	visitedSchemas := make(map[*spec.Schema]bool)
	visitedParams := make(map[*spec.Parameter]bool)
	visitedResponses := make(map[*spec.Response]bool)

	// Validate references in definitions
	for defName, schema := range swspec.Definitions {
		schemaCopy := schema
		errs = append(errs, validateSchemaRefs(&schemaCopy, defName, "definition",
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate references in top-level parameters
	for paramName, param := range swspec.Parameters {
		paramCopy := param
		errs = append(errs, validateParameterRefs(&paramCopy, paramName, "top-level parameter",
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate references in top-level responses
	for respName, resp := range swspec.Responses {
		respCopy := resp
		errs = append(errs, validateResponseRefs(&respCopy, respName, "top-level response",
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate references in paths
	if swspec.Paths != nil {
		for path, pathItem := range swspec.Paths.Paths {
			// Validate path-level parameters
			for i, param := range pathItem.Parameters {
				if param.Ref.String() != "" {
					refName := extractRefName(param.Ref.String())
					if !availableParams[refName] {
						errs = append(errs, fmt.Sprintf(
							"$ref to non-existent parameter %q at path %s parameter[%d]",
							param.Ref.String(), path, i))
					}
					continue
				}
				paramCopy := param
				errs = append(errs, validateParameterRefs(&paramCopy, fmt.Sprintf("%s param[%d]", path, i), "path parameter",
					availableDefinitions, availableParams, availableResponses,
					visitedSchemas, visitedParams, visitedResponses)...)
			}

			// Validate operations
			ops := map[string]*spec.Operation{
				"GET":     pathItem.Get,
				"POST":    pathItem.Post,
				"PUT":     pathItem.Put,
				"DELETE":  pathItem.Delete,
				"PATCH":   pathItem.Patch,
				"HEAD":    pathItem.Head,
				"OPTIONS": pathItem.Options,
			}

			for method, op := range ops {
				if op == nil {
					continue
				}
				location := fmt.Sprintf("%s %s", method, path)

				// Validate operation parameters
				for i, param := range op.Parameters {
					if param.Ref.String() != "" {
						refName := extractRefName(param.Ref.String())
						if !availableParams[refName] {
							errs = append(errs, fmt.Sprintf(
								"$ref to non-existent parameter %q at %s parameter[%d]",
								param.Ref.String(), location, i))
						}
						continue
					}
					paramCopy := param
					errs = append(errs, validateParameterRefs(&paramCopy, fmt.Sprintf("%s param[%d]", location, i), "operation parameter",
						availableDefinitions, availableParams, availableResponses,
						visitedSchemas, visitedParams, visitedResponses)...)
				}

				// Validate operation responses
				if op.Responses != nil {
					// Validate default response
					if op.Responses.Default != nil {
						defaultResp := op.Responses.Default
						if defaultResp.Ref.String() != "" {
							refName := extractRefName(defaultResp.Ref.String())
							if !availableResponses[refName] {
								errs = append(errs, fmt.Sprintf(
									"$ref to non-existent response %q at %s default response",
									defaultResp.Ref.String(), location))
							}
						} else {
							respCopy := *defaultResp
							errs = append(errs, validateResponseRefs(&respCopy, fmt.Sprintf("%s default", location), "operation response",
								availableDefinitions, availableParams, availableResponses,
								visitedSchemas, visitedParams, visitedResponses)...)
						}
					}

					// Validate status code responses
					for code, resp := range op.Responses.StatusCodeResponses {
						if resp.Ref.String() != "" {
							refName := extractRefName(resp.Ref.String())
							if !availableResponses[refName] {
								errs = append(errs, fmt.Sprintf(
									"$ref to non-existent response %q at %s status %d",
									resp.Ref.String(), location, code))
							}
							continue
						}
						respCopy := resp
						errs = append(errs, validateResponseRefs(&respCopy, fmt.Sprintf("%s status %d", location, code), "operation response",
							availableDefinitions, availableParams, availableResponses,
							visitedSchemas, visitedParams, visitedResponses)...)
					}
				}
			}
		}
	}

	return errs
}

// validateSchemaRefs recursively validates $ref references within a schema.
func validateSchemaRefs(schema *spec.Schema, contextName, contextType string,
	availableDefinitions, availableParams, availableResponses map[string]bool,
	visitedSchemas map[*spec.Schema]bool,
	visitedParams map[*spec.Parameter]bool,
	visitedResponses map[*spec.Response]bool) []string {
	var errs []string

	s := schema
	if s == nil {
		return errs
	}

	// Check for circular reference
	if visitedSchemas[s] {
		errs = append(errs, fmt.Sprintf("circular $ref detected in %s %q (schema already visited)", contextType, contextName))
		return errs
	}
	visitedSchemas[s] = true

	// Validate schema $ref
	if s.Ref.String() != "" {
		refStr := s.Ref.String()
		refName := extractRefName(refStr)
		if strings.HasPrefix(refStr, "#/definitions/") {
			if !availableDefinitions[refName] {
				errs = append(errs, fmt.Sprintf(
					"$ref to non-existent definition %q in %s %q",
					refStr, contextType, contextName))
			}
		}
	}

	// Validate items schema
	if s.Items != nil && s.Items.Schema != nil {
		errs = append(errs, validateSchemaRefs(s.Items.Schema, contextName+".items", contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}
	if s.Items != nil {
		for i := range s.Items.Schemas {
			schemaPtr := &s.Items.Schemas[i]
			errs = append(errs, validateSchemaRefs(schemaPtr, fmt.Sprintf("%s.items[%d]", contextName, i), contextType,
				availableDefinitions, availableParams, availableResponses,
				visitedSchemas, visitedParams, visitedResponses)...)
		}
	}

	// Validate properties
	for propName, propSchema := range s.Properties {
		propCopy := propSchema
		errs = append(errs, validateSchemaRefs(&propCopy, fmt.Sprintf("%s.%s", contextName, propName), contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate additionalProperties
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		errs = append(errs, validateSchemaRefs(s.AdditionalProperties.Schema, contextName+".additionalProperties", contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate allOf, anyOf, oneOf
	for i, allOfSchema := range s.AllOf {
		schemaPtr := &allOfSchema
		errs = append(errs, validateSchemaRefs(schemaPtr, fmt.Sprintf("%s.allOf[%d]", contextName, i), contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}
	for i, anyOfSchema := range s.AnyOf {
		schemaPtr := &anyOfSchema
		errs = append(errs, validateSchemaRefs(schemaPtr, fmt.Sprintf("%s.anyOf[%d]", contextName, i), contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}
	for i, oneOfSchema := range s.OneOf {
		schemaPtr := &oneOfSchema
		errs = append(errs, validateSchemaRefs(schemaPtr, fmt.Sprintf("%s.oneOf[%d]", contextName, i), contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	// Validate not
	if s.Not != nil {
		errs = append(errs, validateSchemaRefs(s.Not, contextName+".not", contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	return errs
}

// validateParameterRefs validates $ref references within a parameter.
func validateParameterRefs(param *spec.Parameter, contextName, contextType string,
	availableDefinitions, availableParams, availableResponses map[string]bool,
	visitedSchemas map[*spec.Schema]bool,
	visitedParams map[*spec.Parameter]bool,
	visitedResponses map[*spec.Response]bool) []string {
	var errs []string

	p := param
	if p == nil {
		return errs
	}

	if visitedParams[p] {
		errs = append(errs, fmt.Sprintf("circular $ref detected in %s %q (parameter already visited)", contextType, contextName))
		return errs
	}
	visitedParams[p] = true

	// Validate parameter schema
	if p.Schema != nil {
		errs = append(errs, validateSchemaRefs(p.Schema, contextName+".schema", contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	return errs
}

// validateResponseRefs validates $ref references within a response.
func validateResponseRefs(resp *spec.Response, contextName, contextType string,
	availableDefinitions, availableParams, availableResponses map[string]bool,
	visitedSchemas map[*spec.Schema]bool,
	visitedParams map[*spec.Parameter]bool,
	visitedResponses map[*spec.Response]bool) []string {
	var errs []string

	r := resp
	if r == nil {
		return errs
	}

	if visitedResponses[r] {
		errs = append(errs, fmt.Sprintf("circular $ref detected in %s %q (response already visited)", contextType, contextName))
		return errs
	}
	visitedResponses[r] = true

	// Validate response schema
	if r.Schema != nil {
		errs = append(errs, validateSchemaRefs(r.Schema, contextName+".schema", contextType,
			availableDefinitions, availableParams, availableResponses,
			visitedSchemas, visitedParams, visitedResponses)...)
	}

	return errs
}

// extractRefName extracts the name from a $ref string like "#/definitions/Name" -> "Name".
func extractRefName(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ref
}
