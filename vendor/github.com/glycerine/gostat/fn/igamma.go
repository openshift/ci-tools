package fn

import (
	"math"
)

/*
go-fn issue 4 on https://code.google.com/archive/p/go-fn/issues/4

Posted on May 15, 2013 by Happy Elephant

The interface and naming doesn't match the rest of go-fn, but this
otherwise adds working versions of the upper incomplete gamma
function, the inverse of the upper incomplete gamma function,
the exponential integral E1 and the family En. There are certainly
several optimizations still possible/needed - large alpha, for instance,
is not handled well. These have also not been thoroughly tested, either,
 but are numerically sound in the regions I have tried them (negative
and positive alpha in the region -3, 3, large and small x).
*/

/*
 * Function: UpperIncompleteGamma
 * Arguments: alpha, x float64
 * Limitations: x >= 0.0
 * Returns: z float64, converged bool
 * The upper incomplete gamma function defined by the integral:
                       inf
                     /
                     [     alpha - 1   - y
            z    =   I    y           e    dy
                     ]
                     /
                      x
 * Implementation for large x ( x > abs(alpha) + 1.0 based on a continued
 * fraction and for small x ( x <= abs(alpha) + 1.0 ) based on a power series form given in The
 * Digital Library of Mathematical Functions. Specifically equation 8.9.2 and 8.7.3 at:
 * http://dlmf.nist.gov/8.9.E2
 * http://dlmf.nist.gov/8.7.E3
 * Special Cases:
 * UpperIncompleteGamma( alpha <= 0.0, x < 0.0 ) = NaN
 * UpperIncompleteGamma( alpha, x == 0.0 ) = math.Gamma( alpha )
 * UpperIncompleteGamma( alpha < Inf, x == Inf ) = 0.0
 * UpperIncompleteGamma( alpha == Inf, x < Inf ) = Inf
 * UpperIncompleteGamma( alpha == +/- Inf, x == Inf ) = NaN
 * UpperIncompleteGamma( alpha, x == NaN ) = NaN
 * UpperIncompleteGamma( alpha == NaN, x ) = NaN
 * UpperIncompleteGamma( alpha == -Inf, x >= 1.0 ) = 0.0
 * UpperIncompleteGamma( alpha == -Inf, x < 1.0 ) = Inf
*/
func UpperIncompleteGamma(alpha float64, x float64) (z float64, converged bool) {
	converged = true

	if math.IsNaN(x) || math.IsNaN(alpha) || (alpha < 0.0 && x < 0.0) ||
		((math.IsInf(alpha, 1) || math.IsInf(alpha, -1)) && math.IsInf(x, 1)) {
		z = math.NaN()

	} else if (alpha <= 0.0 && x == 0.0) || math.IsInf(alpha, 1) ||
		(math.IsInf(alpha, -1) && x < 1.0) {
		z = math.Inf(1)

	} else if math.IsInf(x, 1) || (math.IsInf(alpha, -1) && x >= 1.0) {
		z = 0.0

	} else if alpha > 0.0 && x == 0.0 {
		z = math.Gamma(alpha)

	} else if math.Abs(alpha)+1.0 > x && math.Floor(alpha) == alpha && alpha <= 0.0 {
		z, converged = ExponentialIntegraln(x, -int(alpha)+1)
		z *= math.Pow(x, alpha)

	} else if math.Abs(alpha)+1.0 > x {
		z = 0.0

		for order := seriesMax; order >= 0; order-- {
			z = fma(z, x, 1.0/math.Gamma(alpha+float64(order+1)))
		}

		z *= math.Exp(-x) * math.Pow(x, alpha)
		z -= 1.0
		z *= -math.Gamma(alpha)

	} else {
		converged = false

		xinv := 1.0 / x
		var Bjm1, Bj, Ajm1, Aj float64 = 0.0, 1.0, 1.0, 0.0
		Ajm1, Aj = Aj, fma(xinv, Ajm1, Aj)
		Bjm1, Bj = Bj, fma(xinv, Bjm1, Bj)

		vjm1 := Aj / Bj
		var a, i, mantissa, vj float64
		var e, e0 int

		checkIdx := int32(1)
		for iters := int64(1); iters < maxIter; iters++ {

			i = float64(iters)
			a = (i - alpha) * xinv
			Ajm1, Aj = Aj, fma(a, Ajm1, Aj)
			Bjm1, Bj = Bj, fma(a, Bjm1, Bj)

			a = i * xinv
			Ajm1, Aj = Aj, fma(a, Ajm1, Aj)
			Bjm1, Bj = Bj, fma(a, Bjm1, Bj)

			// Prevent overflow without floating point divides by exponent shifting.
			mantissa, e0 = math.Frexp(Bj)
			Bj = math.Ldexp(mantissa, 0)

			mantissa, e = math.Frexp(Bjm1)
			Bjm1 = math.Ldexp(mantissa, e-e0)

			mantissa, e = math.Frexp(Aj)
			Aj = math.Ldexp(mantissa, e-e0)

			mantissa, e = math.Frexp(Ajm1)
			Ajm1 = math.Ldexp(mantissa, e-e0)

			checkIdx++
			checkIdx %= maxCheckIdx
			if checkIdx != 0 {
			} else {
				vj = Aj / Bj

				if math.Abs(vj-vjm1) > fracTol*math.Abs(vj) {
					vjm1 = vj
				} else {
					z = vj * math.Pow(x, alpha) * math.Exp(-x)
					converged = true
					break
				}
			}
		}
	}

	return
}

const seriesMax int32 = 18

/*
 * Function: UpperIncompleteGammaInv
 * Arguments: alpha, z float64
 * Limitations: z >= 0.0 && z <= Gamma( max( +0, alpha ) )
 * Returns: x float64, converged bool
 * The inverse of the upper incomplete gamma function defined by the integral:
                        inf
                     /
                     [     alpha - 1   - y
            z    =   I    y           e    dy
                     ]
                     /
                      x
 * Implementation uses Halley's Method iteration to solve the definition for x.
 * Special Cases:
 * UpperIncompleteGammaInv( alpha, z < 0.0 || z > Gamma( max( +0, alpha) ) ) = NaN
 * UpperIncompleteGammaInv( alpha < Inf, z == Inf ) = 0.0
 * UpperIncompleteGammaInv( alpha == +/-Inf, z ) = NaN
 * UpperIncompleteGammaInv( alpha, z == NaN ) = NaN
 * UpperIncompleteGammaInv( alpha == NaN, z ) = NaN
 * UpperIncompleteGammaInv( alpha <= 0.0, z == 0.0 ) = Inf
*/
func UpperIncompleteGammaInv(alpha, z float64) (x float64, converged bool) {
	converged = true

	zmax := math.Gamma(math.Max(+0, alpha))
	if math.IsNaN(z) || math.IsNaN(alpha) || math.IsInf(alpha, 1) || math.IsInf(alpha, -1) ||
		z < 0.0 || z > zmax {
		x = math.NaN()

	} else if math.IsInf(z, 1) {
		x = 0.0

	} else if alpha <= 0.0 && math.Abs(z) == 0.0 {
		x = math.Inf(1)

	} else {
		converged = false
		posalpha := math.Abs(alpha)
		var guess, lastguess, f, fp, fpp_over_fp float64
		if z < posalpha {
			guess = math.Abs(math.Log(z))
		} else {
			guess = math.Pow(z, 1.0/alpha)
		}

		p := alpha - 1.0
		var uigConv bool
		for iters := int64(0); iters < maxIter; iters++ {
			lastguess = guess

			f, uigConv = UpperIncompleteGamma(alpha, guess)
			if uigConv == false || math.IsNaN(f) {
				x = math.NaN()
				break
			}
			if math.IsInf(f, +1) {
				x = 0.0
				converged = true
				break
			}

			f -= z
			if math.Abs(f) == 0.0 {
				x = guess
				converged = true
				break
			}

			fp = -math.Pow(guess, p) * math.Exp(-guess)
			fpp_over_fp = (p - guess) / guess

			guess = fma(-f, 1.0/(fp-0.5*f*fpp_over_fp), guess)
			if guess < 0.0 { //Prevent search from going negative
				guess = math.Abs(guess)
			} else if math.Abs(guess-lastguess) <= fracTol*math.Abs(guess) ||
				math.IsInf(guess, 1) {
				x = guess
				converged = true
				break
			}
		}
	}

	return
}

// go version of of the C math.h standard function fma()
func fma(x, y, z float64) float64 {
	return (x * y) + z
}

const (
	eulerMascheroniConst float64 = 0.577215664901532860606512090082402431042159335939923598805767234884867726777664670936947063291746749 // AA001620
)

/*
 * Returns the floating point 64 bit version of the Euler-Mascheroni constant.
 */

func Cgamma() float64 {
	return eulerMascheroniConst
}

var maxIter int64 = 1024
var fracTol float64 = math.Pow(2.0, -54) //Default requires exactness
var maxCheckIdx int32 = 4                // Controls number of loops between checks for convergence of continued fractions

func SetMaxIter(i int64) {
	maxIter = i
	return
}

func SetFracTol(t float64) {
	fracTol = t
	return
}

/*
 * Function: ExponentialIntegral1
 * Aliases: E1
 * Arguments: x float64
 * Limitations: x >= 0.0
 * Returns: z float64
 * The exponential integral is defined as:
                                  inf
                                /      - t
                                [    %e
                     E1(x)  =   I    ------ dt
                                ]       t
                                /
                                 x
 * Implementation for small x based on the power series and large x on the
 * continued fraction given in The Digital Library of Mathematical Functions.
 * Specifically equations 6.6.2 and 6.9.1 at:
 * http://dlmf.nist.gov/6.6.E2
 * http://dlmf.nist.gov/6.9.E1
 * Special Cases:
 * ExponentialIntegral1( x == 0.0 ) = Inf
 * ExponentialIntegral1( x == + Inf ) = 0.0
 * ExponentialIntegral1( x < 0.0 ) = NaN
 * ExponentialIntegral1( x == NaN ) = NaN
*/
const ( //iota fails in this context
	E1a1  float64 = +1.0
	E1a2  float64 = -1.0 / 4.0
	E1a3  float64 = +1.0 / 18.0
	E1a4  float64 = -1.0 / 96.0
	E1a5  float64 = +1.0 / 600.0
	E1a6  float64 = -1.0 / 4320.0
	E1a7  float64 = +1.0 / 35280.0
	E1a8  float64 = -1.0 / 322560.0
	E1a9  float64 = +1.0 / 3265920.0
	E1a10 float64 = -1.0 / 36288000.0
	E1a11 float64 = +1.0 / 439084800.0
	E1a12 float64 = -1.0 / 5748019200.0
	E1a13 float64 = +1.0 / 80951270400.0
	E1a14 float64 = -1.0 / 1220496076800.0
	E1a15 float64 = -1.0 / 19615115520000.0
	E1a16 float64 = +1.0 / 334764638208000.0
	E1a17 float64 = -1.0 / 6046686277632000.0
	E1a18 float64 = +1.0 / 115242726703104000.0
	E1a19 float64 = -1.0 / 2311256907767808000.0
)

func ExponentialIntegral1(x float64) (z float64, converged bool) {
	converged = true

	if math.IsNaN(x) || x < 0.0 {
		z = math.NaN()

	} else if x == 0.0 {
		z = math.Inf(1)

	} else if x <= 1.0 {
		z = fma(fma(fma(fma(fma(fma(fma(E1a19, x, E1a18), x, E1a17), x, E1a16), x, E1a15), x, E1a14), x, E1a13), x, E1a12)
		z = fma(fma(fma(fma(fma(fma(fma(z, x, E1a11), x, E1a10), x, E1a9), x, E1a8), x, E1a7), x, E1a6), x, E1a5)
		z = fma(fma(fma(fma(fma(z, x, E1a4), x, E1a3), x, E1a2), x, E1a1), x, -math.Log(x)-eulerMascheroniConst)

	} else {
		converged = false

		xinv := 1.0 / x
		var Bjm1, Bj, Ajm1, Aj float64 = 0.0, 1.0, 1.0, 0.0
		Ajm1, Aj = Aj, fma(xinv, Ajm1, Aj)
		Bjm1, Bj = Bj, fma(xinv, Bjm1, Bj)

		vjm1 := Aj / Bj
		var a, i, mantissa, vj float64
		var e, e0 int
		checkIdx := int32(1)
		for iters := int64(1); iters < maxIter; iters++ {
			i = float64(iters)
			a = i * xinv
			Ajm1, Aj = Aj, fma(a, Ajm1, Aj)
			Bjm1, Bj = Bj, fma(a, Bjm1, Bj)

			//a = i * xinv
			Ajm1, Aj = Aj, fma(a, Ajm1, Aj)
			Bjm1, Bj = Bj, fma(a, Bjm1, Bj)

			// Prevent overflow without floating point divides by exponent shifting.
			mantissa, e0 = math.Frexp(Bj)
			Bj = math.Ldexp(mantissa, 0)

			mantissa, e = math.Frexp(Bjm1)
			Bjm1 = math.Ldexp(mantissa, e-e0)

			mantissa, e = math.Frexp(Aj)
			Aj = math.Ldexp(mantissa, e-e0)

			mantissa, e = math.Frexp(Ajm1)
			Ajm1 = math.Ldexp(mantissa, e-e0)

			checkIdx++
			checkIdx %= maxCheckIdx
			if checkIdx != 0 {
			} else {
				vj = Aj / Bj

				if math.Abs(vj-vjm1) > fracTol*math.Abs(vj) {
					vjm1 = vj
				} else {
					z = vj * math.Exp(-x)
					converged = true
					break
				}
			}
		}
	}

	return
}

/*
 * Function: ExponentialIntegraln
 * Aliases: En
 * Arguments: x float64, n int
 * Limitations: x >= 0.0, n >= 1
 * Returns: z float64
 * The exponential integral is defined as:
                                  inf
                                /      - t
                                [    %e
                     En(x)  =   I    ------ dt
                                ]       t^n
                                /
                                 x
 * Implementation defined recursively using the identity given by equation 12 at:
 * http://mathworld.wolfram.com/En-Function.html
 * and the E1 function.
 * Special Cases:
 * ExponentialIntegraln( x, n < 1 ) = NaN
 * ExponentialIntegraln( x == 0.0, n >=1 ) = Inf
 * ExponentialIntegraln( x == + Inf, n >= 1 ) = 0.0
 * ExponentialIntegraln( x < 0.0, n ) = NaN
 * ExponentialIntegraln( x == NaN, n ) = NaN
*/

func ExponentialIntegraln(x float64, n int) (z float64, converged bool) {
	converged = true

	if math.IsNaN(x) || n < 1 || x < 0.0 {
		z = math.NaN()

	} else if math.IsInf(x, 1) {
		z = 0.0

	} else if x == 0.0 {
		z = math.Inf(1)

	} else if n > 1 {
		z, converged = ExponentialIntegraln(x, n-1)
		z = -fma(z, x, -math.Exp(-x)) / float64(n-1)

	} else {
		z, converged = ExponentialIntegral1(x)
	}

	return
}
