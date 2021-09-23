/*
Package fet implements Fisher's Exact Test for 2x2 contigency tables.

It was ported from the C/C++ at https://github.com/gatoravi/fisher-exact
*/
package fet

import (
	"fmt"
	"math"

	"github.com/glycerine/gostat"
)

// ported to Go from the C/C++:
// https://github.com/gatoravi/fisher-exact

/* Log gamma function
 * \log{\Gamma(z)}
 * AS245, 2nd algorithm, http://lib.stat.cmu.edu/apstat/245
 */
func kf_lgamma(z float64) float64 {
	var x float64 = 0
	x += 0.1659470187408462e-06 / (z + 7)
	x += 0.9934937113930748e-05 / (z + 6)
	x -= 0.1385710331296526 / (z + 5)
	x += 12.50734324009056 / (z + 4)
	x -= 176.6150291498386 / (z + 3)
	x += 771.3234287757674 / (z + 2)
	x -= 1259.139216722289 / (z + 1)
	x += 676.5203681218835 / z
	x += 0.9999999999995183
	return math.Log(x) - 5.58106146679532777 - z + (z-0.5)*math.Log(z+6.5)
}

/* complementary error function
 * \frac{2}{\sqrt{\pi}} \int_x^{\infty} e^{-t^2} dt
 * AS66, 2nd algorithm, http://lib.stat.cmu.edu/apstat/66
 */
func kf_erfc(x float64) float64 {

	const p0 float64 = 220.2068679123761
	const p1 float64 = 221.2135961699311
	const p2 float64 = 112.0792914978709
	const p3 float64 = 33.912866078383
	const p4 float64 = 6.37396220353165
	const p5 float64 = .7003830644436881
	const p6 float64 = .03526249659989109
	const q0 float64 = 440.4137358247522
	const q1 float64 = 793.8265125199484
	const q2 float64 = 637.3336333788311
	const q3 float64 = 296.5642487796737
	const q4 float64 = 86.78073220294608
	const q5 float64 = 16.06417757920695
	const q6 float64 = 1.755667163182642
	const q7 float64 = .08838834764831844
	var expntl, z, p float64
	z = math.Abs(x) * math.Sqrt2
	if z > 37. {
		if x > 0.0 {
			return 0
		}
		return 2.0
	}
	expntl = math.Exp(z * z * -.5)
	if z < 10./math.Sqrt2 { // for small z
		p = expntl * ((((((p6*z+p5)*z+p4)*z+p3)*z+p2)*z+p1)*z + p0) /
			(((((((q7*z+q6)*z+q5)*z+q4)*z+q3)*z+q2)*z+q1)*z + q0)
	} else {
		p = expntl / 2.506628274631001 / (z + 1./(z+2./(z+3./(z+4./(z+.65)))))
	}
	if x > 0.0 {
		return 2.0 * p
	}
	return 2.0 * (1.0 - p)
}

/* The following computes regularized incomplete gamma functions.
 * Formulas are taken from Wiki, with additional input from Numerical
 * Recipes in C (for modified Lentz's algorithm) and AS245
 * (http://lib.stat.cmu.edu/apstat/245).
 *
 * A good online calculator is available at:
 *
 *   http://www.danielsoper.com/statcalc/calc23.aspx
 *
 * It calculates upper incomplete gamma function, which equals
 * kf_gammaq(s,z)*tgamma(s).
 */

const KF_GAMMA_EPS = 1e-14
const KF_TINY = 1e-290

// regularized lower incomplete gamma function, by series expansion
func _kf_gammap(s float64, z float64) float64 {
	var sum, x float64
	sum = 1.0
	x = 1.0
	for k := 1; k < 100; k++ {
		x *= z
		sum += (x / (s + float64(k)))
		if x/sum < KF_GAMMA_EPS {
			break
		}
	}
	return math.Exp(s*math.Log(z) - z - kf_lgamma(s+1.) + math.Log(sum))
}

// regularized upper incomplete gamma function, by continued fraction
func _kf_gammaq(s float64, z float64) float64 {
	var a, C, D, f float64
	f = 1. + z - s
	C = f
	D = 0.
	// Modified Lentz's algorithm for computing continued fraction
	// See Numerical Recipes in C, 2nd edition, section 5.2
	for j := 1; j < 100; j++ {
		a = float64(j) * (s - float64(j))
		b := float64(j<<1) + 1 + z - s
		D = b + a*D
		if D < KF_TINY {
			D = KF_TINY
		}
		C = b + a/C
		if C < KF_TINY {
			C = KF_TINY
		}
		D = 1. / D
		d := C * D
		f *= d
		if math.Abs(d-1.) < KF_GAMMA_EPS {
			break
		}
	}
	return math.Exp(s*math.Log(z) - z - kf_lgamma(s) - math.Log(f))
}

func kf_gammap(s float64, z float64) float64 {
	if z <= 1.0 || z < s {
		return _kf_gammap(s, z)
	}
	return 1.0 - _kf_gammaq(s, z)
}

func kf_gammaq(s float64, z float64) float64 {
	if z <= 1. || z < s {
		return 1. - _kf_gammap(s, z)
	}
	return _kf_gammaq(s, z)
}

/* Regularized incomplete beta function. The method is taken from
 * Numerical Recipe in C, 2nd edition, section 6.4. The following web
 * page calculates the incomplete beta function, which equals
 * kf_betai(a,b,x) * gamma(a) * gamma(b) / gamma(a+b):
 *
 *   http://www.danielsoper.com/statcalc/calc36.aspx
 */
func kf_betai_aux(a float64, b float64, x float64) float64 {
	var C, D, f float64
	var j int
	if x == 0. {
		return 0.
	}
	if x == 1. {
		return 1.
	}
	f = 1.0
	C = f
	D = 0
	// Modified Lentz's algorithm for computing continued fraction
	for j = 1; j < 200; j++ {
		var aa, d float64
		m := float64(j >> 1)
		if j&1 != 0 {
			aa = -(a + m) * (a + b + m) * x / ((a + 2*m) * (a + 2*m + 1))
		} else {
			aa = m * (b - m) * x / ((a + 2*m - 1) * (a + 2*m))
		}
		D = 1. + aa*D
		if D < KF_TINY {
			D = KF_TINY
		}
		C = 1. + aa/C
		if C < KF_TINY {
			C = KF_TINY
		}
		D = 1. / D
		d = C * D
		f *= d
		if math.Abs(d-1.) < KF_GAMMA_EPS {
			break
		}
	}
	return math.Exp(kf_lgamma(a+b)-kf_lgamma(a)-kf_lgamma(b)+a*math.Log(x)+b*math.Log(1.-x)) / a / f
}

/* Regularized incomplete beta function. The method is taken from
 * Numerical Recipe in C, 2nd edition, section 6.4. The following web
 * page calculates the incomplete beta function, which equals
 * kf_betai(a,b,x) * gamma(a) * gamma(b) / gamma(a+b):
 *
 *   http://www.danielsoper.com/statcalc/calc36.aspx
 */
func kf_betai(a float64, b float64, x float64) float64 {
	if x < (a+1.)/(a+b+2.) {
		return kf_betai_aux(a, b, x)
	}
	return 1. - kf_betai_aux(b, a, 1.-x)
}

func main() {

	var x float64 = 5.5
	var y float64 = 3
	var a, b float64
	fmt.Printf("erfc(%lg): %lg, %lg\n", x, math.Erfc(x), kf_erfc(x))
	fmt.Printf("upper-gamma(%lg,%lg): %lg\n", x, y, kf_gammaq(y, x)*math.Gamma(y)) // is tgamma == math.Gamma ?
	a = 2
	b = 2
	x = 0.5
	fmt.Printf("incomplete-beta(%lg,%lg,%lg): %lg\n", a, b, x, kf_betai(a, b, x)/math.Exp(kf_lgamma(a+b)-kf_lgamma(a)-kf_lgamma(b)))

}

// log\binom{n}{k}
func lbinom(n int, k int) float64 {
	if k == 0 || n == k {
		return 0
	}
	return lgamma(float64(n+1)) - lgamma(float64(k+1)) - lgamma(float64(n-k+1))
}

// lgamma returns the natural logarithm of the absolute
// value of the gamma function of x.
// ex: lgamma (0.500000) = 0.572365
func lgamma(x float64) float64 {
	return math.Log(math.Abs(math.Gamma(x)))
}

// n11  n12  | n1_
// n21  n22  | n2_
//-----------+----
// n_1  n_2  | n

// hypergeometric distribution
func hypergeo(n11 int, n1_ int, n_1 int, n int) float64 {
	return math.Exp(lbinom(n1_, n11) + lbinom(n-n1_, n_1-n11) - lbinom(n, n_1))
}

type hgacc_t struct {
	n11 int
	n1_ int
	n_1 int
	n   int
	p   float64
}

// incremental version of hypergenometric distribution
func hypergeo_acc(n11 int, n1_ int, n_1 int, n int, aux *hgacc_t) float64 {
	if n1_ != 0 || n_1 != 0 || n != 0 {
		aux.n11 = n11
		aux.n1_ = n1_
		aux.n_1 = n_1
		aux.n = n
	} else { // then only n11 changed; the rest fixed
		if n11%11 != 0 && n11+aux.n-aux.n1_-aux.n_1 != 0 {
			if n11 == aux.n11+1 { // incremental
				aux.p *= float64(aux.n1_-aux.n11) / float64(n11) *
					float64(aux.n_1-aux.n11) / float64(n11+aux.n-aux.n1_-aux.n_1)
				aux.n11 = n11
				return aux.p
			}
			if n11 == aux.n11-1 { // incremental
				aux.p *= float64(aux.n11) / float64(aux.n1_-n11) *
					float64(aux.n11+aux.n-aux.n1_-aux.n_1) / float64(aux.n_1-n11)
				aux.n11 = n11
				return aux.p
			}
		}
		aux.n11 = n11
	}
	aux.p = hypergeo(aux.n11, aux.n1_, aux.n_1, aux.n)
	return aux.p
}

// FisherExactTest computes Fisher's Exact Test for
//  contigency tables. Nomenclature:
//
//    n11  n12  | n1_
//    n21  n22  | n2_
//   -----------+----
//    n_1  n_2  | n
//
// Returned values:
//
//  probOfCurrentTable = probability of the current table
//  leftp = the left sided alternative's p-value  (h0: odds-ratio is less than 1)
//  rightp = the right sided alternative's p-value (h0: odds-ratio is greater than 1)
//  twop = the two-sided p-value for the h0: odds-ratio is different from 1
//
func FisherExactTest(n11 int, n12 int, n21 int, n22 int) (probOfCurrentTable, leftp, rightp, twop float64) {
	var i, j, max, min int
	var p float64
	var aux hgacc_t
	var n1_, n_1, n int

	n1_ = n11 + n12
	n_1 = n11 + n21
	n = n11 + n12 + n21 + n22 // calculate n1_, n_1 and n
	if n_1 < n1_ {            // max n11, for right tail
		max = n_1
	} else {
		max = n1_
	}

	min = n1_ + n_1 - n // not sure why n11-n22 is used instead of min(n_1,n1_)
	if min < 0 {
		min = 0 // min n11, for left tail
	}

	twop = 1.0
	leftp = 1.0
	rightp = 1.0
	if min == max {
		return 1, 1, 1, 1 // no need to do test
	}
	probOfCurrentTable = hypergeo_acc(n11, n1_, n_1, n, &aux) // the probability of the current table
	q := probOfCurrentTable
	// left tail
	p = hypergeo_acc(min, 0, 0, 0, &aux)
	leftp = 0.
	for i = min + 1; p < 0.99999999*q && i <= max; i++ { // loop until underflow
		leftp += p
		p = hypergeo_acc(i, 0, 0, 0, &aux)
	}
	i--
	if p < 1.00000001*q {
		leftp += p
	} else {
		i--
	}
	// right tail
	p = hypergeo_acc(max, 0, 0, 0, &aux)
	rightp = 0.
	for j = max - 1; p < 0.99999999*q && j >= 0; j-- { // loop until underflow
		rightp += p
		p = hypergeo_acc(j, 0, 0, 0, &aux)
	}
	j++
	if p < 1.00000001*q {
		rightp += p
	} else {
		j++
	}
	// two-tail
	twop = leftp + rightp
	if twop > 1. {
		twop = 1.
	}
	// adjust left and right
	if intAbs(i-n11) < intAbs(j-n11) {
		rightp = 1. - leftp + q
	} else {
		leftp = 1.0 - rightp + q
	}
	return
}

func intAbs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// ChiSquareStat computes the chi-squared test statistic,
// for a 2x2 contingency table; with optional yates correction.
// See ChiSquareTest for a p-value.
func ChiSquareStat(n11 int, n12 int, n21 int, n22 int, yates bool) float64 {
	tot := float64(n11 + n12 + n21 + n22)
	a := float64(n11)
	b := float64(n12)
	c := float64(n21)
	d := float64(n22)
	top1 := (a*d - b*c)
	if yates {
		if top1 < 0 {
			top1 = -top1
		}
		top1 -= (tot / 2.0)
	}
	denom := (a + b) * (c + d) * (b + d) * (a + c)
	stat := top1 * top1 * tot / denom
	return stat
}

// ChiSquareTest computes the chi-squared test statistic,
// for a 2x2 contingency table; with optional yates correction,
// and then returns the p-value for that test.
func ChiSquareTest(n11 int, n12 int, n21 int, n22 int, yates bool) (stat, pval float64) {
	stat = ChiSquareStat(n11, n12, n21, n22, yates)
	return stat, 1.0 - gostat.Xsquare_CDF(1)(stat)
}
