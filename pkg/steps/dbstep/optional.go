package dbstep

import (
	"fmt"
	"sort"
)

// optional are the schemes whose drivers are left out of a default build, and
// the build tag that puts each back. Each driver is several megabytes, and
// PostgreSQL, MySQL and SQLite are what most suites need:
//
//	go build ./cmd/cli                        # postgres, mysql, sqlite
//	go build -tags db_mongodb ./cmd/cli       # ... and MongoDB
//	go build -tags db_all ./cmd/cli           # every database; make build-full
//
// The language does not change with the build: the checker accepts every
// scheme, and a step naming one this binary was built without is a step error
// saying how to build one with it.
var optional = map[string]string{
	"sqlserver":   "db_sqlserver",
	"oracle":      "db_oracle",
	"mongodb":     "db_mongodb",
	"mongodb+srv": "db_mongodb",
	"redis":       "db_redis",
	"rediss":      "db_redis",
}

// Unbuilt returns the schemes artemis knows and this binary was built without,
// sorted.
func Unbuilt() []string {
	built := map[string]bool{}
	for _, s := range Schemes() {
		built[s] = true
	}
	var out []string
	for s := range optional {
		if !built[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// unbuiltError is the error for a scheme this binary was built without, and
// nil for any other.
func unbuiltError(scheme string) error {
	tag, ok := optional[scheme]
	if !ok {
		return nil
	}
	return fmt.Errorf("this artemis was built without the %s driver; build one with it: "+
		"make build-full, or go build -tags %s ./cmd/cli", scheme, tag)
}
