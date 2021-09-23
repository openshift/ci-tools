// Copyright 2012 - 2013 The Fn Authors. All rights reserved. See the LICENSE file.

package fn

//The Gamma function and relatives.

import (
	"math"
)

//Natural logarithm of the Gamma function
func LnGamma(x float64) (res float64) {
	res = (x-0.5)*math.Log(x+4.5) - (x + 4.5)
	res += logsqrt2pi
	res += math.Log1p(
		76.1800917300/(x+0) - 86.5053203300/(x+1) +
			24.0140982200/(x+2) - 1.23173951600/(x+3) +
			0.00120858003/(x+4) - 0.00000536382/(x+5))

	return
}

// NB: See igamma.go and UpperIncompleteGamma() for new implementation.

/*
//Upper incomplete Gamma function	DOES NOT WORK FOR FLOAT, ONLY INT S, needs to be reimplemented
func IΓ(s, x float64) float64 {
	if s < 0 {
		return 1
	}
	return (s-1) * IΓ(s-1, x) + math.Pow(x, s-1) * math.Exp(-x)
}
*/

//Upper incomplete Gamma function	// did not pass test for IΓ(1.45896, 3.315) == 0.0706743424609074192334
//func IΓ(s, x float64) float64 {
//	return IGamC(s, x) * Γ(s)
//}

func IΓ(s, x float64) float64 {
	// rewrite using the new code in igamma.go
	//return IGamC(s, x) * Γ(s)
	val, converged := UpperIncompleteGamma(s, x)
	if !converged {
		panic("UpperIncompleteGamma(s,x) did not converge")
	}
	return val
}

//Upper incomplete Gamma function for integer s only
func IΓint(s int64, x float64) float64 {
	if s < 0 {
		return 1
	}
	return float64(s-1)*IΓint(s-1, x) + math.Pow(x, float64(s-1))*math.Exp(-x)
}

/*
//Lower incomplete Gamma function   BUGGY!!!
func Iγ(s, x float64) float64 {
	if s < 0 {
		return 1
	}
	return (s-1) * Iγ(s-1, x) - math.Pow(x, s-1) * math.Exp(-x)
}
*/

//Lower incomplete Gamma function
func Iγ(s, x float64) float64 {
	if s < 0 {
		return 1
	}
	return IGam(s, x) * Γ(s)

	// https://en.wikipedia.org/wiki/Incomplete_gamma_function can be computed as:
	// math.Exp(GAMMALN(s)) * GAMMA.DIST(x,s,beta=1,TRUE)
	// where
	//       GAMMA.DIST: If beta = 1, GAMMA.DIST returns the standard gamma distribution.
	//   and GAMMA.DIST: last param TRUE => cumulative distribution function
	// and where
	//       GAMMALN(s): Returns the natural logarithm of the gamma function, Γ(x).
}

//Lower incomplete Gamma function for integer s only
func Iγint(s int64, x float64) float64 {
	if s < 0 {
		return 1
	}
	return Γ(float64(s)) - IΓint(s, x)
}

// Regularized Gamma function
func Γr(s, x float64) float64 {
	return Iγ(s, x) / Γ(s)
}

func GammaP(p int, x float64) (r float64) {
	pf := float64(p)
	r = math.Pow(math.Pi, 0.25*pf*(pf-1))
	for j := float64(1); j <= pf; j++ {
		r *= GammaF(x + .5*(1-j))
	}
	return
}

func LnGammaP(p int, x float64) (r float64) {
	pf := float64(p)
	r = pf * (pf - 1) * .25 * math.Log(math.Pi)
	for j := float64(1); j <= pf; j++ {
		r += LnGamma(x + .5*(1-j))
	}
	return
}

func GammaPRatio(p int, x, y float64) (r float64) {
	pf := float64(p)
	for j := float64(1); j <= pf; j++ {
		r *= GammaF(x + .5*(1-j))
		r /= GammaF(y + .5*(1-j))
	}
	return
}

//LnGammap(x)/LnGammap(y)
func LnGammaPRatio(p int, x, y float64) (r float64) {
	pf := float64(p)
	for j := float64(1); j <= pf; j++ {
		r += LnGamma(x + .5*(1-j))
		r -= LnGamma(y + .5*(1-j))
	}
	return
}

////////////////////
func lgammafn(x float64) float64 {

	/* For IEEE double precision DBL_EPSILON = 2^-52 = 2.220446049250313e-16 :
	   xmax  = DBL_MAX / log(DBL_MAX) = 2^1024 / (1024 * log(2)) = 2^1014 / log(2)
	   dxrel = sqrt(DBL_EPSILON) = 2^-26 = 5^26 * 1e-26 (is *exact* below !)
	*/
	const (
		xmax  = 2.5327372760800758e+305
		dxrel = 1.490116119384765696e-8
	)

	if isNaN(x) {
		return x
	}
	if x <= 0 && x == trunc(x) { /* Negative integer argument */
		return posInf /* +Inf, since lgamma(x) = log|gamma(x)| */
	}

	y := abs(x)

	if y < 1e-306 { // denormalized range
		return -log(x)
	}
	if y <= 10 {
		return log(abs(math.Gamma(x)))
	}

	//   ELSE  y = |x| > 10

	if y > xmax {
		return posInf
	}

	if x > 0 { /* i.e. y = x > 10 */
		if x > 1e17 {
			return (x * (log(x) - 1))
		} else if x > 4934720. {
			return (lnSqrt2π + (x-0.5)*log(x) - x)
		} else {
			return lnSqrt2π + (x-0.5)*log(x) - x + lgammacor(x)
		}
	}
	/* else: x < -10; y = -x */
	sinpiy := abs(sin(π * y))

	if sinpiy == 0 { // Negative integer argument
		//	  Now UNNECESSARY: caught above, should NEVER happen!
		return nan
	}

	ans := lnSqrtπd2 + (x-0.5)*log(y) - x - log(sinpiy) - lgammacor(y)

	if abs((x-trunc(x-0.5))*ans/x) < dxrel {

		panic("precision")
	}

	return ans
}
