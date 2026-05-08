package persistence

import (
	"fmt"

	"github.com/Masterminds/squirrel"
)

func Exists(subTable string, cond squirrel.Sqlizer) squirrel.Sqlizer {
	return existsCond{subTable: subTable, cond: cond}
}

func NotExists(subTable string, cond squirrel.Sqlizer) squirrel.Sqlizer {
	return existsCond{subTable: subTable, cond: cond, not: true}
}

type existsCond struct {
	subTable string
	cond     squirrel.Sqlizer
	not      bool
}

func (e existsCond) ToSql() (string, []any, error) {
	sql, args, err := e.cond.ToSql()
	sql = fmt.Sprintf("exists (select 1 from %s where %s)", e.subTable, sql)
	if e.not {
		sql = "not " + sql
	}
	return sql, args, err
}
