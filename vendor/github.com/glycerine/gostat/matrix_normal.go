package gostat

import (
	"fmt"
	mx "github.com/skelterjohn/go.matrix"
	"math"
)

func checkMatrixNormal(M, Omega, Sigma *mx.DenseMatrix) {
	p := M.Rows()
	m := M.Cols()
	if Omega.Rows() != p {
		panic(fmt.Sprintf("Omega.Rows != M.Rows, %d != %d", Omega.Rows(), p))
	}
	if Omega.Cols() != Omega.Rows() {
		panic("Omega is not square")
	}
	if Sigma.Rows() != m {
		panic(fmt.Sprintf("Sigma.Cols != M.Cols, %d != %d", Sigma.Cols(), m))
	}
	if Sigma.Cols() != Sigma.Rows() {
		panic("Sigma is not square")
	}
}

/*
 M is the mean, Omega is the row covariance, Sigma is the column covariance.
*/
func MatrixNormal_PDF(M, Omega, Sigma *mx.DenseMatrix) func(A *mx.DenseMatrix) float64 {
	checkMatrixNormal(M, Omega, Sigma)
	pf := float64(M.Rows())
	mf := float64(M.Cols())

	norm := math.Pow(2*math.Pi, -0.5*mf*pf)
	norm *= math.Pow(Omega.Det(), -0.5*mf)
	norm *= math.Pow(Sigma.Det(), -0.5*pf)

	return func(X *mx.DenseMatrix) (p float64) {
		p = norm

		sinv, err := Sigma.Inverse()
		if err != nil {
			panic(err)
		}
		oinv, err := Omega.Inverse()
		if err != nil {
			panic(err)
		}
		diff, err := X.MinusDense(M)
		if err != nil {
			panic(err)
		}
		inner := oinv

		inner, err = inner.TimesDense(diff.Transpose())
		if err != nil {
			panic(err)
		}

		inner, err = inner.TimesDense(sinv)
		if err != nil {
			panic(err)
		}

		inner, err = inner.TimesDense(diff)
		if err != nil {
			panic(err)
		}

		innerTrace := inner.Trace()

		p *= math.Exp(-0.5 * innerTrace)

		return
	}
}
func MatrixNormal_LnPDF(M, Omega, Sigma *mx.DenseMatrix) func(A *mx.DenseMatrix) float64 {
	checkMatrixNormal(M, Omega, Sigma)

	pf := float64(M.Rows())
	mf := float64(M.Cols())

	sinv, err := Sigma.Inverse()
	if err != nil {
		panic(err)
	}
	oinv, err := Omega.Inverse()
	if err != nil {
		panic(err)
	}

	norm := (2 * math.Pi) * (-0.5 * mf * pf)
	norm += Omega.Det() * (-0.5 * mf)
	norm += Sigma.Det() * (-0.5 * pf)

	return func(X *mx.DenseMatrix) (lp float64) {
		lp = norm
		diff, err := X.MinusDense(M)
		if err != nil {
			panic(err)
		}
		inner := oinv

		inner, err = inner.TimesDense(diff.Transpose())
		if err != nil {
			panic(err)
		}

		inner, err = inner.TimesDense(sinv)
		if err != nil {
			panic(err)
		}

		inner, err = inner.TimesDense(diff)
		if err != nil {
			panic(err)
		}

		innerTrace := inner.Trace()

		lp += -0.5 * innerTrace

		return
	}
}
func MatrixNormal(M, Omega, Sigma *mx.DenseMatrix) func() (X *mx.DenseMatrix) {
	checkMatrixNormal(M, Omega, Sigma)

	Mv := mx.Vectorize(M)
	Cov := mx.Kronecker(Omega, Sigma)
	normal := MVNormal(Mv, Cov)
	return func() (X *mx.DenseMatrix) {
		Xv := normal()
		X = mx.Unvectorize(Xv, M.Rows(), M.Cols())
		return
	}
}
func NextMatrixNormal(M, Omega, Sigma *mx.DenseMatrix) (X *mx.DenseMatrix) {
	return MatrixNormal(M, Omega, Sigma)()
}
