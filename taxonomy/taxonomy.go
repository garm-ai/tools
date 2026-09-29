// Package taxonomy names the compartments and tool sets the garm-ai/tools
// packages declare, as Go constants, so a service or a test can refer to a
// name without spelling it and a misspelling fails to compile rather than
// silently naming a compartment nobody holds.
//
// The declarations themselves live in proto/tools/taxonomy/v1/taxonomy.proto
// and travel into a catalogue from there; these constants are the same
// strings, and taxonomy_test.go asserts that they stay the same.
package taxonomy

import (
	// Linked so the file descriptor is registered: a test or a service that
	// imports this package can read the declarations off it.
	_ "github.com/garm-ai/tools/taxonomy/gen/tools/taxonomy/v1"
)

// Compartments.
const (
	// CompartmentInternet: reaching hosts outside the tenant over the
	// public internet.
	CompartmentInternet = "internet"
	// CompartmentGeneratedArtefacts: files a tool produced during a run.
	CompartmentGeneratedArtefacts = "generated-artefacts"
)

// Tool sets.
const (
	// SetResearch: reading the outside world.
	SetResearch = "research"
	// SetDocuments: producing and reading files.
	SetDocuments = "documents"
)
