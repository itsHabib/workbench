package query

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// Options controls reduction and bounded output. The limit never truncates scans.
type Options struct {
	Group, Sum        string
	Top               string
	Limit             int
	CountOnly, Scalar bool
}

// Group is one reduction bucket. Nil keys are missing values, not empty strings.
type Group struct {
	Key        any    `json:"key"`
	Count      int64  `json:"count"`
	Sum        *int64 `json:"sum,omitempty"`
	SumPresent int64  `json:"sum_present,omitempty"`
}

// Result contains exact totals plus bounded projected rows or groups.
type Result struct {
	Matches     int64            `json:"matches"`
	Sum         *int64           `json:"sum,omitempty"`
	SumPresent  int64            `json:"sum_present,omitempty"`
	Groups      []Group          `json:"groups,omitempty"`
	Rows        []map[string]any `json:"rows,omitempty"`
	TotalGroups int              `json:"total_groups,omitempty"`
	Truncated   bool             `json:"truncated"`
}

// Execute reduces all matching rows; integer sums fail rather than overflow.
func (p *Plan) Execute(o Options) (Result, error) {
	top, group, sum, err := p.reductionColumns(o)
	if err != nil {
		return Result{}, err
	}
	r := Result{}
	if sum != nil {
		r.Sum = new(int64)
	}
	buckets := make(map[string]*Group)
	ranked := topRows{column: top, limit: o.Limit}
	for i, v := range p.Match(o.Scalar) {
		if v == 0 {
			continue
		}
		r.Matches++
		if err := addSum(r.Sum, &r.SumPresent, sum, i); err != nil {
			return Result{}, err
		}
		if group != nil {
			if err := addGroup(buckets, group, sum, i); err != nil {
				return Result{}, err
			}
			continue
		}
		if top != nil {
			ranked.add(i)
			continue
		}
		if !o.CountOnly && len(r.Rows) < o.Limit {
			r.Rows = append(r.Rows, p.table.row(i))
		}
	}
	if top != nil {
		for _, row := range ranked.sorted() {
			r.Rows = append(r.Rows, p.table.row(row))
		}
	}
	if group != nil {
		r.finishGroups(buckets, o.Limit)
		return r, nil
	}
	r.Truncated = !o.CountOnly && r.Matches > int64(len(r.Rows))
	return r, nil
}

func (p *Plan) reductionColumns(o Options) (top, group, sum *Column, err error) {
	if o.Limit < 0 {
		return nil, nil, nil, fmt.Errorf("limit must be nonnegative")
	}
	if o.Top != "" && (o.Group != "" || o.CountOnly) {
		return nil, nil, nil, fmt.Errorf("top cannot be combined with group or count")
	}
	top, err = optionalColumn(p.table, o.Top)
	if err != nil {
		return nil, nil, nil, err
	}
	if top != nil && top.Type != "int" {
		return nil, nil, nil, fmt.Errorf("top requires an integer column")
	}
	group, err = optionalColumn(p.table, o.Group)
	if err != nil {
		return nil, nil, nil, err
	}
	sum, err = optionalColumn(p.table, o.Sum)
	if err != nil {
		return nil, nil, nil, err
	}
	if sum != nil && sum.Type != "int" {
		return nil, nil, nil, fmt.Errorf("sum requires an integer column")
	}
	return top, group, sum, nil
}

func optionalColumn(t *Table, name string) (*Column, error) {
	if name == "" {
		return nil, nil
	}
	return t.Find(name)
}

func addSum(total *int64, present *int64, col *Column, row int) error {
	if col == nil || col.Valid[row] == 0 {
		return nil
	}
	v := col.Values[row]
	if v > 0 && *total > math.MaxInt64-v || v < 0 && *total < math.MinInt64-v {
		return fmt.Errorf("sum of %s overflows int64", col.Name)
	}
	*total += v
	*present++
	return nil
}

func addGroup(groups map[string]*Group, col, sum *Column, row int) error {
	key := col.Value(row)
	encoded, _ := json.Marshal(key)
	g := groups[string(encoded)]
	if g == nil {
		g = &Group{Key: key}
		if sum != nil {
			g.Sum = new(int64)
		}
		groups[string(encoded)] = g
	}
	g.Count++
	return addSum(g.Sum, &g.SumPresent, sum, row)
}

func (r *Result) finishGroups(groups map[string]*Group, limit int) {
	for _, g := range groups {
		r.Groups = append(r.Groups, *g)
	}
	sort.Slice(r.Groups, func(i, j int) bool {
		if r.Groups[i].Count != r.Groups[j].Count {
			return r.Groups[i].Count > r.Groups[j].Count
		}
		a, _ := json.Marshal(r.Groups[i].Key)
		b, _ := json.Marshal(r.Groups[j].Key)
		return string(a) < string(b)
	})
	r.TotalGroups = len(r.Groups)
	if len(r.Groups) > limit {
		r.Groups = r.Groups[:limit]
		r.Truncated = true
	}
}

func (t *Table) row(row int) map[string]any {
	m := make(map[string]any, len(t.Columns))
	for _, c := range t.Columns {
		m[c.Name] = c.Value(row)
	}
	return m
}
