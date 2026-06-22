// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	flags "github.com/jessevdk/go-flags"

	"github.com/go-openapi/analysis"
	"github.com/go-openapi/loads"
	"github.com/go-openapi/spec"

	"github.com/go-swagger/go-swagger/generator"
)

const (
	mergeNothingToDoMsg        = "nothing to do. Need some swagger files to merge.\nUSAGE: swagger merge <primary-swagger-file> <mixin-swagger-file...>"
	mergePathConflictMsg       = "endpoint path conflicts detected"
	minRequiredMergeArgs       = 2
	exitCodeOnMergePathConflicts = 253
)

// pathConflict represents a conflicting endpoint path.
type pathConflict struct {
	Method string
	Path   string
	Source string
}

// MergeSpec holds command line flag definitions specific to the merge command.
type MergeSpec struct {
	Compact        bool           `description:"applies to JSON formatted specs. When present, doesn't prettify the json" long:"compact"`
	Output         flags.Filename `description:"the file to write to"                                                                 long:"output"           short:"o"`
	KeepSpecOrder  bool           `description:"Keep schema properties order identical to spec file"                                    long:"keep-spec-order"`
	Format         string         `choice:"yaml"                                                                                       choice:"json"           default:"json" description:"the format for the spec document" long:"format"`
	IgnoreConflicts bool          `description:"Ignore path conflicts and continue merging"                                             long:"ignore-conflicts"`
}

// Execute runs the merge command which merges multiple Swagger specs into one,
// failing on endpoint path conflicts and listing all conflicting routes.
func (c *MergeSpec) Execute(args []string) error {
	if len(args) < minRequiredMergeArgs {
		return errors.New(mergeNothingToDoMsg)
	}

	log.Printf("args[0] = %v\n", args[0])
	log.Printf("args[1:] = %v\n", args[1:])

	conflicts, err := c.MergeFiles(args[0], args[1:], os.Stdout)

	for _, warn := range conflicts.warnings {
		log.Println(warn)
	}

	if err != nil {
		return err
	}

	if len(conflicts.pathConflicts) > 0 && !c.IgnoreConflicts {
		return fmt.Errorf("%s:\n%s", mergePathConflictMsg, formatPathConflicts(conflicts.pathConflicts))
	}

	return nil
}

// mergeConflicts holds the result of a merge operation.
type mergeConflicts struct {
	warnings      []string
	pathConflicts []pathConflict
}

// MergeFiles reads the given swagger files, merges mixins into primary,
// detects endpoint path conflicts, and writes the result.
//
// Returns the warnings, path conflicts, and any error.
func (c *MergeSpec) MergeFiles(primaryFile string, mixinFiles []string, _ io.Writer) (mergeConflicts, error) {
	var result mergeConflicts

	primaryDoc, err := loads.Spec(primaryFile)
	if err != nil {
		return result, err
	}
	primary := primaryDoc.Spec()

	primaryPaths, primaryPathOriginals := collectEndpointPaths(primary, primaryFile)

	mixins := make([]*spec.Swagger, 0, len(mixinFiles))
	for _, mixinFile := range mixinFiles {
		if c.KeepSpecOrder {
			mixinFile = generator.WithAutoXOrder(mixinFile)
		}
		mixin, lerr := loads.Spec(mixinFile)
		if lerr != nil {
			return result, lerr
		}

		mixinPaths, mixinPathOriginals := collectEndpointPaths(mixin.Spec(), mixinFile)
		for method, paths := range mixinPaths {
			for normalizedPath, source := range paths {
				if primaryPathsMethod, ok := primaryPaths[method]; ok {
					if originalPrimaryPath, exists := primaryPathsMethod[normalizedPath]; exists {
						// Found a conflict, use the original path from mixin for display
						originalMixinPath := mixinPathOriginals[method][normalizedPath]
						result.pathConflicts = append(result.pathConflicts, pathConflict{
							Method: method,
							Path:   fmt.Sprintf("%s (equivalent to %s from %s)", originalMixinPath, originalPrimaryPath, primaryFile),
							Source: source,
						})
					}
				}
			}
		}
		// Merge mixin paths into primary paths for subsequent conflict detection
		for method, paths := range mixinPaths {
			if _, ok := primaryPaths[method]; !ok {
				primaryPaths[method] = make(map[string]string)
				primaryPathOriginals[method] = make(map[string]string)
			}
			for normalizedPath := range paths {
				primaryPaths[method][normalizedPath] = mixinFile
				primaryPathOriginals[method][normalizedPath] = mixinPathOriginals[method][normalizedPath]
			}
		}

		mixins = append(mixins, mixin.Spec())
	}

	warnings := analysis.Mixin(primary, mixins...)
	result.warnings = warnings
	analysis.FixEmptyResponseDescriptions(primary)

	return result, writeToFile(primary, !c.Compact, c.Format, string(c.Output))
}

// collectEndpointPaths collects all endpoint paths from a swagger spec.
// Returns two maps:
//   - normalized: method -> normalized path -> source (for conflict detection)
//   - originals: method -> normalized path -> original path (for display)
//
// Path normalization handles equivalent path parameter syntax:
//   - /users/:id and /users/{userId} both normalize to /users/{param}
func collectEndpointPaths(swspec *spec.Swagger, source string) (map[string]map[string]string, map[string]map[string]string) {
	normalized := make(map[string]map[string]string)
	originals := make(map[string]map[string]string)

	if swspec == nil || swspec.Paths == nil {
		return normalized, originals
	}

	addPath := func(method, path string) {
		norm := normalizePath(path)
		if _, ok := normalized[method]; !ok {
			normalized[method] = make(map[string]string)
			originals[method] = make(map[string]string)
		}
		normalized[method][norm] = source
		originals[method][norm] = path
	}

	for path, pathItem := range swspec.Paths.Paths {
		// GET
		if pathItem.Get != nil {
			addPath("GET", path)
		}
		// POST
		if pathItem.Post != nil {
			addPath("POST", path)
		}
		// PUT
		if pathItem.Put != nil {
			addPath("PUT", path)
		}
		// DELETE
		if pathItem.Delete != nil {
			addPath("DELETE", path)
		}
		// PATCH
		if pathItem.Patch != nil {
			addPath("PATCH", path)
		}
		// HEAD
		if pathItem.Head != nil {
			addPath("HEAD", path)
		}
		// OPTIONS
		if pathItem.Options != nil {
			addPath("OPTIONS", path)
		}
	}

	return normalized, originals
}

// normalizePath normalizes a URL path for equivalence comparison.
// It replaces all path parameter segments with a canonical placeholder,
// making /users/:id and /users/{userId} equivalent.
//
// Supported parameter syntaxes:
//   - {name}   - OpenAPI/Swagger style
//   - :name    - Express/Sinatra style
//   - <name>   - Flask/Werkzeug style
func normalizePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		// OpenAPI/Swagger style: {paramName}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			segments[i] = "{param}"
			continue
		}
		// Express/Sinatra style: :paramName
		if strings.HasPrefix(seg, ":") {
			segments[i] = "{param}"
			continue
		}
		// Flask/Werkzeug style: <paramName> or <converter:paramName>
		if strings.HasPrefix(seg, "<") && strings.HasSuffix(seg, ">") {
			segments[i] = "{param}"
			continue
		}
	}
	return strings.Join(segments, "/")
}

// formatPathConflicts formats path conflicts for display.
func formatPathConflicts(conflicts []pathConflict) string {
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Method != conflicts[j].Method {
			return conflicts[i].Method < conflicts[j].Method
		}
		return conflicts[i].Path < conflicts[j].Path
	})

	var lines []string
	for _, c := range conflicts {
		lines = append(lines, fmt.Sprintf("  - %s %s (from %s)", c.Method, c.Path, c.Source))
	}

	return strings.Join(lines, "\n")
}
