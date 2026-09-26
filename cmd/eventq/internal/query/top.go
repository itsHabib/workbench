package query

import (
	"container/heap"
	"sort"
)

// The heap retains at most limit rows. Root is the worst retained row; ties
// favor earlier source order. Missing values sort after all present integers.
type topRows struct {
	rows   []int
	column *Column
	limit  int
}

func (t topRows) Len() int           { return len(t.rows) }
func (t topRows) Less(i, j int) bool { return t.worse(t.rows[i], t.rows[j]) }
func (t topRows) Swap(i, j int)      { t.rows[i], t.rows[j] = t.rows[j], t.rows[i] }
func (t *topRows) Push(v any)        { t.rows = append(t.rows, v.(int)) }
func (t *topRows) Pop() any          { i := len(t.rows) - 1; v := t.rows[i]; t.rows = t.rows[:i]; return v }
func (t topRows) worse(a, b int) bool {
	if t.column.Valid[a] != t.column.Valid[b] {
		return t.column.Valid[a] == 0
	}
	if t.column.Valid[a] != 0 && t.column.Values[a] != t.column.Values[b] {
		return t.column.Values[a] < t.column.Values[b]
	}
	return a > b
}
func (t *topRows) add(row int) {
	if t.limit == 0 {
		return
	}
	if t.Len() < t.limit {
		heap.Push(t, row)
		return
	}
	if t.worse(t.rows[0], row) {
		t.rows[0] = row
		heap.Fix(t, 0)
	}
}
func (t *topRows) sorted() []int {
	sort.Slice(t.rows, func(i, j int) bool { return t.worse(t.rows[j], t.rows[i]) })
	return t.rows
}
