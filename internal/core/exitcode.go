package core

// Process exit codes. These are part of the public CLI contract (see PRD F17)
// and must not change meaning between releases.
const (
	ExitOK          = 0   // everything succeeded
	ExitPartial     = 1   // finished, but some repos failed
	ExitUsage       = 2   // bad flags, arguments or configuration
	ExitLocked      = 3   // another run holds the archive lock
	ExitEnvironment = 4   // git missing or too old, unusable system
	ExitInterrupted = 130 // stopped by Ctrl+C / SIGINT
)

// ExitCodeFor maps an error to the exit code the CLI should return.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	switch CategoryOf(err) {
	case CatConfig:
		return ExitUsage
	case CatEnvironment:
		return ExitEnvironment
	}
	return ExitPartial
}
