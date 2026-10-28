package structure

// Change describes a function-level edit in the new file's line coordinates.
type Change struct {
	Kind, Name         string
	StartLine, EndLine int
}
