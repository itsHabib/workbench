//go:build !go1.27 || !goexperiment.simd

package query

// Backend describes the compiled scan kernel.
const Backend = "scalar"

func filter(mask, values, valid []int64, op string, value int64) {
	scalarFilter(mask, values, valid, op, value)
}
