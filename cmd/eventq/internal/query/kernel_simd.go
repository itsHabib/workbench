//go:build go1.27 && goexperiment.simd

package query

import "simd"

// Backend describes the compiled scan kernel; Go chooses hardware or emulation.
const Backend = "simd-experimental"

func filter(mask, values, valid []int64, op string, value int64) {
	bound := simd.BroadcastInt64s(value)
	// Presence and selection lanes are always 0/-1, so their sum is -2
	// exactly when both are true. Avoid a three-way integer And here:
	// Go 1.27.1 emits invalid AVX-512 VPTERNLOGQ for that expression.
	bothTrue := simd.BroadcastInt64s(-2)
	width := bound.Len()
	i := 0
	switch op {
	case "==":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Equal(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	case "!=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).NotEqual(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	case ">":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Greater(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	case ">=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).GreaterEqual(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	case "<":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Less(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	case "<=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).LessEqual(bound)
			eligible := simd.LoadInt64s(valid[i : i+width]).Add(simd.LoadInt64s(mask[i : i+width])).Equal(bothTrue)
			matches.And(eligible).ToInt64s().Store(mask[i : i+width])
		}
	}
	// For null/present, i remains zero and the scalar kernel handles all rows.
	scalarFilter(mask[i:], values[i:], valid[i:], op, value)
}
