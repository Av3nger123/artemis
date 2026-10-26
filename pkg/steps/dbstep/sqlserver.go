//go:build db_sqlserver || db_all

package dbstep

import _ "github.com/microsoft/go-mssqldb" // registers "sqlserver"

func init() { Register("sqlserver", sqlDriver{sqlServer}) }
