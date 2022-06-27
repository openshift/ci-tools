#!/usr/bin/env python3

# THIS IS NOT PART OF THE LAMBDA EXECUTION. IT IS USED TO GENERATE DATA FOR
# THE LAMBDA. COPY OUTPUT ARRAY INTO lambda_function.py.

# Outputs an array[[ip_start, ip_stop], [ip_start, ip_stop], ...] for
# all EC2 CIDR ranges in us-east-1. This is used to inform our CloudFront
# function and should be pasted directly into its code.

# In short, we want:
# 1. all us-east-1 AWS based registry access to use VPC gateway endpoints  (free)
# 2. all us-east-2 AWS based registry access to use s3 transfer (0.01 plus NAT costs)
# 3. all other traffic to go through CloudFront (our discount + NAT costs on client side)

# Understanding #1
# OpenShift's vanilla install in AWS includes a VPC gateway endpoint
# for S3 which allows hosts within a private subnet (all OCP instances)
# to communicate to S3 at zero cost to an S3 bucket when it is in the same region. If
# an instance does not use this endpoint, traffic is processed through
# the NAT -- which is very expensive. Our build farm S3 registry buckets are in
# us-east-1. So for all us-east-1 IP addresses, redirect the caller to use
# the S3 hostname -- ensuring traffic goes through the inexpensive S3 VPC endpoint.
#
# Understanding #2
# When an EC2 instance in another region and a private subnet makes an S3 request, the data
# transfers through the NAT -- which is expensive. In addition to the NAT cost, s3 transfer
# costs are incurred by the serving bucket.
# https://cloudfix.com/resources/s3-vpc-endpoints-deep-dive/ . There is no way to avoid this
# NAT cost, but for us-east-2, the S3 transfer cost (0.01) is less than our CloudFront price.
#
# Understanding #3
# For non-us-east-1 and non-us-east-2, go through CloudFront. This eliminates S3 data transfer
# costs, but keeps the NAT processing cost on the client side if it is in
# AWS. For non-AWS clusters, always go through CloudFront. S3 transferring
# out to the Internet ranges between 0.09 and 0.05 per GB -- our CloudFront
# is less expensive than this.
#
#
# The goal of this script is to help our CloudFront viewer request function (lambda_function.py)
# rapidly determine whether an incoming request is from a specific EC2 region. If it is, the function
# will redirect the client back to S3. If it is not, the request will be fulfilled through
# CloudFront.
#
# The AWS IP ranges will change periodically. Our redirections do not have to be 100% accurate.
# both CloudFront and S3 will resolve. We just need the statistically majority of AWS
# requests to go through S3.
#
# It may be worth re-running this script once in awhile to refresh the IP ranges in the
# nodejs CloudFront Viewer request Lambda@Edge function.

import urllib.request
from ipaddress import ip_network, ip_address
import json

AWS_IP_RANGES_URL = "https://ip-ranges.amazonaws.com/ip-ranges.json"

with urllib.request.urlopen(AWS_IP_RANGES_URL) as f:
    cidrs = json.load(f)['prefixes']

ranges_to_reroute = []
for cb in cidrs:

    service = cb['service']
    if service.lower() != "ec2":
        continue

    region = cb['region']
    if region.startswith('us-east-1') is False and region.startswith('us-east-2') is False:
        # See generate_range_array for more details.
        continue

    prefix = cb['ip_prefix']
    net = ip_network(prefix)

    first_address_decimal = int(ip_address(net[0]))
    last_address_decimal = int(ip_address(net[-1]))
    ranges_to_reroute.append([first_address_decimal, last_address_decimal])

print(f'Found {len(ranges_to_reroute)} qualifying ranges')

# Sort from low to high in order to allow an optimized search in lambda_function.py
ranges_to_reroute.sort(key=lambda entry: entry[0])

print(json.dumps(ranges_to_reroute))
