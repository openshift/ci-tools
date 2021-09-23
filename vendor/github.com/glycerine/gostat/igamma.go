// Inverse Gamma distribution (not to be confused with Inverse CDF of Gamma distribution)

package gostat

import (
	"math"

	. "github.com/glycerine/gostat/fn"
)

// Inverse Gamma distribution: probability density function
func InvGamma_PDF(a, b float64) func(x float64) float64 {
	return func(x float64) float64 {
		return math.Exp(a*math.Log(b) - LnGamma(a) - (a+1)*math.Log(x) - b*1.0/x)
	}
}

// Inverse Gamma distribution: natural logarithm of the probability density function
func InvGamma_LnPDF(a, b float64) func(x float64) float64 {
	return func(x float64) float64 {
		return a*math.Log(b) - LnGamma(a) - (a+1)*math.Log(x) - b*1.0/x
	}
}

// Inverse Gamma distribution: probability density function at x
func InvGamma_PDF_At(a, b float64) func(x float64) float64 {
	return func(x float64) float64 {
		return math.Exp(a*math.Log(b) - LnGamma(a) - (a+1)*math.Log(x) - b*1.0/x)
	}
}

// Inverse Gamma distribution: cumulative distribution function
func InvGamma_CDF(a, b float64) func(x float64) float64 {
	return func(x float64) float64 {
		return 1 - IΓ(a, b*1.0/x)
	}
}

// Inverse Gamma distribution: value of the cumulative distribution function at x
func InvGamma_CDF_At(a, b, x float64) float64 {
	cdf := InvGamma_CDF(a, b)
	return cdf(x)
}
