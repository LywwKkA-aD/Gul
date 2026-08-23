package config

import "fmt"

// migration lifts a settings document one schema version forward, in place.
// Steps work on the generic document rather than on Config: a field a later
// schema dropped no longer has a struct to live in.
type migration func(doc map[string]any)

// migrations maps a schema version to the step that lifts it to the next one.
// Version 0 is a document without a version field - everything written before
// the schema was numbered.
var migrations = map[int]migration{
	0: migrateV0ToV1,
}

// migrateV0ToV1 keeps a pre-schema document: every field it can carry has the
// same meaning in v1, and what it lacks is filled from the defaults by the
// decoder. The step exists so the chain covers version 0 rather than treating
// an unnumbered document as unreadable.
func migrateV0ToV1(map[string]any) {}

// migrate runs the chain from the document's own version up to SchemaVersion.
// The table is a parameter so a test can drive a chain that does not exist in
// this build. A gap in it is a build defect, not a user problem, but it is
// reported like any other unreadable document: defaults, file left alone.
func migrate(doc map[string]any, from int, steps map[int]migration) error {
	for version := from; version < SchemaVersion; version++ {
		step, ok := steps[version]
		if !ok {
			return fmt.Errorf("%w: no migration from version %d", ErrUnsupportedVersion, version)
		}
		step(doc)
	}
	return nil
}
