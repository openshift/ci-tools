package gostat

import (
	. "github.com/glycerine/gostat/fn"
	m "github.com/skelterjohn/go.matrix"
)

func Wishart_PDF(n int, V *m.DenseMatrix) func(W *m.DenseMatrix) float64 {
	p := V.Rows()
	Vdet := V.Det()
	Vinv, _ := V.Inverse()
	normalization := pow(2, -0.5*float64(n*p)) *
		pow(Vdet, -0.5*float64(n)) /
		Γ(0.5*float64(n))
	return func(W *m.DenseMatrix) float64 {
		VinvW, _ := Vinv.Times(W)
		return normalization * pow(W.Det(), 0.5*float64(n-p-1)) *
			exp(-0.5*VinvW.Trace())
	}
}
func Wishart_LnPDF(n int, V *m.DenseMatrix) func(W *m.DenseMatrix) float64 {

	p := V.Rows()
	Vdet := V.Det()
	Vinv, _ := V.Inverse()
	normalization := log(2)*(-0.5*float64(n*p)) +
		log(Vdet)*(-0.5*float64(n)) -
		LnGamma(0.5*float64(n))
	return func(W *m.DenseMatrix) float64 {
		VinvW, _ := Vinv.Times(W)
		return normalization +
			log(W.Det())*0.5*float64(n-p-1) -
			0.5*VinvW.Trace()
	}
}
func NextWishart(n int, V *m.DenseMatrix) *m.DenseMatrix {
	return Wishart(n, V)()
}
func Wishart(n int, V *m.DenseMatrix) func() *m.DenseMatrix {
	p := V.Rows()
	zeros := m.Zeros(p, 1)
	rowGen := MVNormal(zeros, V)
	return func() *m.DenseMatrix {
		x := make([][]float64, n)
		for i := 0; i < n; i++ {
			x[i] = rowGen().Array()
		}
		X := m.MakeDenseMatrixStacked(x)
		S, _ := X.Transpose().TimesDense(X)
		return S
	}
}

func InverseWishart_PDF(n int, Ψ *m.DenseMatrix) func(B *m.DenseMatrix) float64 {
	p := Ψ.Rows()
	Ψdet := Ψ.Det()
	normalization := pow(Ψdet, -0.5*float64(n)) *
		pow(2, -0.5*float64(n*p)) /
		Γ(float64(n)/2)
	return func(B *m.DenseMatrix) float64 {
		Bdet := B.Det()
		Binv, _ := B.Inverse()
		ΨBinv, _ := Ψ.Times(Binv)
		return normalization *
			pow(Bdet, -.5*float64(n+p+1)) *
			exp(-0.5*ΨBinv.Trace())
	}
}
func InverseWishart_LnPDF(n int, Ψ *m.DenseMatrix) func(W *m.DenseMatrix) float64 {
	p := Ψ.Rows()
	Ψdet := Ψ.Det()
	normalization := log(Ψdet)*-0.5*float64(n) +
		log(2)*-0.5*float64(n*p) -
		LnGamma(float64(n)/2)
	return func(B *m.DenseMatrix) float64 {
		Bdet := B.Det()
		Binv, _ := B.Inverse()
		ΨBinv, _ := Ψ.Times(Binv)
		return normalization +
			log(Bdet)*-.5*float64(n+p+1) +
			-0.5*ΨBinv.Trace()
	}
}
func NextInverseWishart(n int, V *m.DenseMatrix) *m.DenseMatrix {
	return InverseWishart(n, V)()
}
func InverseWishart(n int, V *m.DenseMatrix) func() *m.DenseMatrix {
	p := V.Rows()
	zeros := m.Zeros(p, 1)
	rowGen := MVNormal(zeros, V)
	return func() *m.DenseMatrix {
		x := make([][]float64, n)
		for i := 0; i < n; i++ {
			x[i] = rowGen().Array()
		}
		X := m.MakeDenseMatrixStacked(x)
		S, _ := X.Transpose().TimesDense(X)
		Sinv, _ := S.Inverse()
		return Sinv
	}
}
