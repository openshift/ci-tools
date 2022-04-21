#!/usr/bin/env python

from scipy.stats import fisher_exact
import numpy
import matplotlib
from matplotlib import cm
import matplotlib.pyplot as plt


# We want to determine if the two samples (the corpus from accepted releases and
# the smaller set of results from the testing of a PR) come from binary distributions
# that are dissimilar (have different underlying percentages). We use the Fisher exact
# test, as our sample sizes are small: https://www.itl.nist.gov/div898/handbook/prc/section3/prc33.htm

fig = plt.figure()
ax = fig.add_subplot(111)

significance = 0.05
corpus_size = 250

for test_size in [12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1]:
	corpus_percentages = numpy.arange(0, 101, 1)
	test_positive_counts = numpy.arange(0, test_size+1, 1)
	min_positive_counts = []
	
	for corpus_percentage in corpus_percentages:
		found = False
		for test_positive_count in test_positive_counts:
			corpus_positive_count = round(corpus_size * corpus_percentage/100)
			corpus_negative_count = corpus_size - corpus_positive_count
			test_negative_count = test_size - test_positive_count
			# leave this debugging line for david because he doesn't speak python
 			# print("checking %d %d %d %d\n" % (corpus_positive_count, test_positive_count, corpus_negative_count, test_negative_count))

			_, p = fisher_exact([
				[corpus_positive_count, test_positive_count],
				[corpus_negative_count, test_negative_count],
				], alternative="greater")

			if p <= significance:
				continue
			else:
				# if we get here then we went one past significance
				if test_positive_count == 0:
					min_positive_counts.append(test_positive_count)
				else:
					min_positive_counts.append(test_positive_count-1)
				# leave this debugging line for david because he doesn't speak python
				# print("percent %d, min_positive_count %d" % (corpus_percentage, test_positive_count))
				found = True
				break
		if not found:
			if corpus_percentage == 100:
				min_positive_counts.append(min_positive_counts[98])
			else:
				min_positive_counts.append(0)

	for i, required_pass in enumerate(min_positive_counts):
		if i % 10 == 0:
			print()
		print("%d, " % (required_pass), end='')
		if i % 10 == 9:
			print(" // %d-%d" % (i-9, i), end='')
	print()

# _, p = fisher_exact([
# 	[125, 1],
# 	[125, 9],
# 	], alternative="greater")
# print(p)
