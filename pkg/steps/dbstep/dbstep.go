package dbstep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// StepType is the type a scenario writes to get this executor.
const StepType = "db"

// DefaultTimeout is how long one attempt may take when the step does not say:
// connecting, if this is the first step to use the URL, and the statement. It
// is the statement's context deadline, so a query that outlives it is
// cancelled where the driver can cancel one.
const DefaultTimeout = 30 * time.Second

// MaxRows is how many rows one attempt may return. More is a step error rather
// than a cut, because a cut `rows` would make `len(rows)` a quiet lie; a query
// that means to read that many wants a LIMIT or a count(*).
const MaxRows = 10_000

// The roots a db step binds, compared against check.Roots(check.DB) in
// observe_test.go for the reason the other step packages compare theirs.
const (
	// RootRows is the rows a query returned: an array of objects keyed by
	// column name, and [] for a statement that returns none.
	RootRows = "rows"
	// RootAffected is how many rows a statement changed, or a query
	// returned, and null when the driver cannot say.
	RootAffected = "affected"
	// RootResult is a command's reply: a Redis reply, a Mongo command's
	// reply document. Null for sql.
	RootResult = "result"
)

// Executor is the db step.
type Executor struct {
	// Timeout is the deadline for a step that does not set one. Zero means
	// DefaultTimeout.
	Timeout time.Duration
}

var _ executor.Executor = Executor{}

func init() {
	executor.Register(StepType, Executor{})
}

func (e Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// Observe runs the step's statement once and reports its rows and its count.
//
// The connection comes from the run's Pools, borrowed and never closed; a
// context with none -- a step run outside `artemis run` -- gets a pool of its
// own for the attempt. A statement that ran is an observation whatever it
// returned; the error return is for one that could not run: no driver for the
// URL, a database that refused the connection, SQL it rejected, the deadline.
func (e Executor) Observe(ctx context.Context, step models.Step, _ executor.Scope) (map[string]any, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}
	spec := step.DB
	if strings.TrimSpace(spec.URL) == "" {
		return nil, errors.New("db step has no connection URL: its value was empty")
	}
	var (
		st   Statement
		args []any
	)
	if spec.Command == nil {
		if st, err = statement(spec); err != nil {
			return nil, err
		}
		if args, err = bind(spec.Args); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pools, shared := PoolsOf(ctx)
	if !shared {
		pools = NewPools()
		defer pools.Close()
	}
	conn, err := pools.Conn(ctx, spec.URL)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("could not connect to %s within %s", Redact(spec.URL), timeout)
		}
		return nil, err
	}
	var res Result
	if spec.Command != nil {
		res, err = conn.Command(ctx, spec.Command)
	} else {
		res, err = conn.SQL(ctx, st, args)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("no result from %s within %s", Redact(spec.URL), timeout)
	}
	if err != nil {
		return nil, err
	}
	return res.roots(), nil
}

// roots is the result under the names the step type binds.
func (r Result) roots() map[string]any {
	rows := r.Rows
	if rows == nil {
		rows = []any{}
	}
	var affected any
	if r.Affected != nil {
		affected = *r.Affected
	}
	return map[string]any{RootRows: rows, RootAffected: affected, RootResult: r.Value}
}

// statement is the SQL the step runs: its text, or the contents of its file --
// run whole, as a script, when it binds no args.
func statement(spec models.DB) (Statement, error) {
	if spec.SQLFile != "" {
		b, err := os.ReadFile(spec.SQLFile)
		if err != nil {
			return Statement{}, fmt.Errorf("sql: %w", err)
		}
		return Statement{Text: string(b), Script: len(spec.Args) == 0}, nil
	}
	if strings.TrimSpace(spec.SQL) == "" {
		return Statement{}, errors.New("db step has no sql to run: its value was empty")
	}
	return Statement{Text: spec.SQL}, nil
}

// bind is the step's args as a driver takes them: a file's bytes in place of
// the file, everything else as lower left it.
func bind(args []any) ([]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		f, ok := a.(models.FileArg)
		if !ok {
			out[i] = a
			continue
		}
		b, err := os.ReadFile(string(f))
		if err != nil {
			return nil, fmt.Errorf("args[%d]: %w", i, err)
		}
		out[i] = b
	}
	return out, nil
}
