package source

import (
	"sort"
	"strings"
)

// Field is a searchable package field.
type Field int

const (
	FieldID Field = iota
	FieldName
	FieldMoniker
	FieldTag
	FieldCommand
)

func (f Field) String() string {
	return [...]string{"Id", "Name", "Moniker", "Tag", "Command"}[f]
}

// MatchType ranks how well a value matched (lower is better).
type MatchType int

const (
	MatchExact MatchType = iota
	MatchCaseInsensitive
	MatchStartsWith
	MatchSubstring
)

// Query describes a search.
type Query struct {
	Term   string
	Fields []Field // fields to search
	Exact  bool    // require (case-insensitive) equality
}

// DefaultSearchFields are used by `search` when no field flag is given.
var DefaultSearchFields = []Field{FieldID, FieldName, FieldMoniker, FieldTag, FieldCommand}

// DefaultInstallFields are used by install/show/download, like winget.
var DefaultInstallFields = []Field{FieldID, FieldName, FieldMoniker}

// Result is a matched package plus why it matched.
type Result struct {
	*Package
	Field      Field
	MatchType  MatchType
	MatchValue string
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Search runs a query against the index. An empty term lists everything.
func (s *Source) Search(q Query) ([]Result, error) {
	best := map[int64]Result{}
	pattern := "%" + likeEscape(q.Term) + "%"
	op := `LIKE ? ESCAPE '\'`
	arg := pattern
	if q.Exact {
		op, arg = `= ? COLLATE NOCASE`, q.Term
	}
	for _, f := range q.Fields {
		var sqlq string
		switch f {
		case FieldID:
			sqlq = `SELECT ` + pkgCols + `, p.id FROM packages p WHERE p.id ` + op
		case FieldName:
			sqlq = `SELECT ` + pkgCols + `, p.name FROM packages p WHERE p.name ` + op
		case FieldMoniker:
			sqlq = `SELECT ` + pkgCols + `, p.moniker FROM packages p WHERE p.moniker ` + op
		case FieldTag:
			sqlq = `SELECT ` + pkgCols + `, t.tag FROM tags2 t JOIN tags2_map m ON m.tag = t.rowid JOIN packages p ON p.rowid = m.package WHERE t.tag ` + op
		case FieldCommand:
			sqlq = `SELECT ` + pkgCols + `, c.command FROM commands2 c JOIN commands2_map m ON m.command = c.rowid JOIN packages p ON p.rowid = m.package WHERE c.command ` + op
		}
		rows, err := s.db.Query(sqlq, arg)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var val string
			p, err := s.scan(rows, &val)
			if err != nil {
				rows.Close()
				return nil, err
			}
			r := Result{Package: p, Field: f, MatchType: classify(val, q.Term), MatchValue: val}
			if old, ok := best[p.RowID]; !ok || better(r, old) {
				best[p.RowID] = r
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Result, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MatchType != out[j].MatchType {
			return out[i].MatchType < out[j].MatchType
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func better(a, b Result) bool {
	if a.MatchType != b.MatchType {
		return a.MatchType < b.MatchType
	}
	return a.Field < b.Field
}

func classify(val, term string) MatchType {
	switch {
	case val == term:
		return MatchExact
	case strings.EqualFold(val, term):
		return MatchCaseInsensitive
	case strings.HasPrefix(strings.ToLower(val), strings.ToLower(term)):
		return MatchStartsWith
	}
	return MatchSubstring
}
