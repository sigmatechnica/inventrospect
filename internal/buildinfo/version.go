package buildinfo

import "fmt"

var Version = "dev"
var Commit = "unknown"
var Date = "unknown"

func String() string {
	return fmt.Sprintf("Inventrospect %s (commit %s, built %s)", Version, Commit, Date)
}
