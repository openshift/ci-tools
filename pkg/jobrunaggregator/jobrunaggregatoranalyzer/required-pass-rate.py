#!/usr/bin/env python

from scipy.stats import fisher_exact
import numpy
import matplotlib
from matplotlib import cm
import matplotlib.pyplot as plt


# We want to determine if the two samples (the corpus from accepted releases and
# the smaller set of results from the testing of a PR) come from binary distributions
# that are dissimilar (have different underlying proportions). We use the Fisher exact
# test, as our sample sizes are small: https://www.itl.nist.gov/div898/handbook/prc/section3/prc33.htm

fig = plt.figure()
ax = fig.add_subplot(111)

test_size = 10
significance = 0.05

for corpus_size in [250]:
	corpus_proportions = numpy.arange(0,1.01,.01)
	test_proportions = numpy.arange(0,1.1,.1)

	@numpy.vectorize
	def P(corpus_proportion, test_proportion, alternative):
		corpus_positive_count = round(corpus_size * corpus_proportion)
		corpus_negative_count = corpus_size - corpus_positive_count
		test_positive_count = round(test_size * test_proportion)
		test_negative_count = test_size - test_positive_count
		_, significance = fisher_exact([
			[corpus_positive_count, test_positive_count],
			[corpus_negative_count, test_negative_count],
			], alternative=alternative)
		return significance

	x, y = numpy.meshgrid(corpus_proportions, test_proportions)

	greater = P(x,y,'greater')
	max_min = []
	for c in range(greater.shape[1]):
		previous = numpy.NAN
		for idx, p in enumerate(greater[:,c]):
			if p <= significance:
				previous = idx / test_size
			else:
				max_min.append(previous)
				break

	# mesh = ax.pcolormesh(x, y, z, vmin=0, vmax=1, cmap=cm.viridis, shading='nearest')
	color = next(ax._get_lines.prop_cycler)['color']
	ax.plot(corpus_proportions, max_min, color=color)
	ax.text(1.005, max_min[-1], 'n={}'.format(corpus_size), color=color)

ax.set_aspect('equal', adjustable='box')
ax.set_ylim(0,1)
plt.ylabel('Observed Test Pass Proportion')
ax.set_xlim(0,1)
plt.xlabel('Observed Corpus Pass Proportion')
plt.title('Test Passes Required For Statistical Significance')
plt.show()