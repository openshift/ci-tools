package gostat

import (
	. "github.com/skelterjohn/go.matrix"
)

func MVNormal_PDF(μ *DenseMatrix, Σ *DenseMatrix) func(x *DenseMatrix) float64 {
	p := μ.Rows()
	backμ := μ.DenseMatrix()
	backμ.Scale(-1)

	Σdet := Σ.Det()
	ΣdetRt := sqrt(Σdet)
	Σinv, _ := Σ.Inverse()

	normalization := pow(2*π, -float64(p)/2) / ΣdetRt

	return func(x *DenseMatrix) float64 {
		δ, _ := x.PlusDense(backμ)
		tmp := δ.Transpose()
		tmp, _ = tmp.TimesDense(Σinv)
		tmp, _ = tmp.TimesDense(δ)
		f := tmp.Get(0, 0)
		return normalization * exp(-f/2)
	}
}
func NextMVNormal(μ *DenseMatrix, Σ *DenseMatrix) *DenseMatrix {
	n := μ.Rows()
	x := Zeros(n, 1)
	for i := 0; i < n; i++ {
		x.Set(i, 0, NextNormal(0, 1))
	}
	C, err := Σ.Cholesky()
	Cx, err := C.TimesDense(x)
	μCx, err := μ.PlusDense(Cx)
	if err != nil {
		panic(err)
	}
	return μCx
}

func MVNormal(μ *DenseMatrix, Σ *DenseMatrix) func() *DenseMatrix {
	C, _ := Σ.Cholesky()
	n := μ.Rows()
	return func() *DenseMatrix {
		x := Zeros(n, 1)
		for i := 0; i < n; i++ {
			x.Set(i, 0, NextNormal(0, 1))
		}
		Cx, _ := C.TimesDense(x)
		MCx, _ := μ.PlusDense(Cx)
		return MCx
	}
}
