//go:build go1.27 && goexperiment.simd

package query

import "simd"

// Backend describes the compiled scan kernel; Go chooses hardware or emulation.
const Backend = "simd-experimental"

func filter(mask, values, valid []int64, op string, value int64) {
	bound := simd.BroadcastInt64s(value)
	width := bound.Len()
	i := 0
	switch op {
	case "==":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Equal(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	case "!=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).NotEqual(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	case ">":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Greater(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	case ">=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).GreaterEqual(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	case "<":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).Less(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	case "<=":
		for ; i+width <= len(values); i += width {
			matches := simd.LoadInt64s(values[i : i+width]).LessEqual(bound).ToInt64s()
			matches.And(simd.LoadInt64s(valid[i : i+width])).And(simd.LoadInt64s(mask[i : i+width])).Store(mask[i : i+width])
		}
	}
	scalarFilter(mask[i:], values[i:], valid[i:], op, value)
}
