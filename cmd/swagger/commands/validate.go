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
