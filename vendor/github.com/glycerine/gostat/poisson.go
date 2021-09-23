// Poisson distribution

package gostat

import (
	"math"

	. "github.com/glycerine/gostat/fn"
)

/*
func Poisson_LnPMF(λ float64) (foo func(i int64) float64) {
	pmf := Poisson_PMF(λ)
	return func(i int64) (p float64) {
		return log(pmf(i))
		//p = -λ +log(λ)*float64(i)
		//x := log(Γ(float64(i)+1))
		//_ = x
		//p -= LnGamma(float64(i)+1)
		//return p
	}
}
*/
func Poisson_LnPMF(λ float64) func(k int64) float64 {
	return func(k int64) (p float64) {
		i := float64(k)
		a := log(λ) * i
		b := log(Γ(i + 1))
		p = a - b - λ
		return p
	}
}

/*
func Poisson_PMF(λ float64) func(k int64) float64 {
	return func(k int64) float64 {
		p := NextExp(-λ) * pow(λ, float64(k)) / Γ(float64(k)+1)
		return p
	}
}

func Poisson_PMF(λ float64) func(k int64) float64 {
	return func(k int64) float64 {
		p := math.Exp(-λ) * pow(λ, float64(k)) / Γ(float64(k)+1)
		return p
	}
}
*/

func Poisson_PMF(λ float64) func(k int64) float64 {
	pmf := Poisson_LnPMF(λ)
	return func(k int64) float64 {
		p := math.Exp(pmf(k))
		return p
	}
}

func Poisson_PMF_At(λ float64, k int64) float64 {
	pmf := Poisson_PMF(λ)
	return pmf(k)
}

func NextPoisson(λ float64) int64 {
	// this can be improved upon
	i := iZero
	t := exp(-λ)
	p := fOne
	for ; p > t; p *= NextUniform() {
		i++
	}
	return i
}
func Poisson(λ float64) func() int64 {
	return func() int64 {
		return NextPoisson(λ)
	}
}

func Poisson_CDF(λ float64) func(k int64) float64 {
	return func(k int64) float64 {
		var p float64 = 0
		var i int64
		pmf := Poisson_PMF(λ)
		for i = 0; i <= k; i++ {
			p += pmf(i)
		}
		return p
	}
}

func Poisson_CDF_a(λ float64) func(k int64) float64 { // analytic solution, less precision
	return func(k int64) float64 {
		p := math.Exp(math.Log(IΓint(k+1, λ)) - (LnFact(float64(k))))
		return p
	}
}

func Poisson_CDF_At(λ float64, k int64) float64 {
	cdf := Poisson_CDF(λ)
	return cdf(k)
}

func LnPoisson_CDF_a(λ float64) func(k int64) float64 { // analytic solution, less precision
	return func(k int64) float64 {
		k1 := (float64)(k + 1)
		return log(IΓ(k1, λ)) - LnFact(float64(k))
	}
}
