//go:build db_oracle || db_all

package dbstep

import _ "github.com/sijms/go-ora/v2" // registers "oracle"

func init() { Register("oracle", sqlDriver{oracle}) }
