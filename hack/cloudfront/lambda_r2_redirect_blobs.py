# The purpose of this lambda is to redirect requests for an image registry blob stored
# in a traditional buildfarm/app.ci internal image registry S3 bucket to a corresponding
# "blob cache" bucket created in CloudFlare R2.
# R2 has zero egress cost, so every time we can redirect a client to a cached blob,
# we are saving the AWS CloudFront/S3 egress costs (cents per GB).
# The starting point for the architecture should be as follows:
# - Cluster setup with S3 bucket and CloudFront middleware for imageregistry.
#   - This means the cluster image registry is writing content to S3 bucket.
#   - Requests from registry clients hit CloudFront first.
#   - CloudFront returns cached content or reads from S3 and streams content to the client.
# - CloudFlare R2 bucket setup which is in "incremental update" mode backed by the S3 bucket.
#   This means that if you attempt to read a file from the R2 bucket and it is not present,
#   R2 will binary from S3 and store it in R2 before returning it to the client.
#   - The R2 bucket should also have a management rule setup to delete files after 30 days (this
#     should be safe to adjust up or down.
#
# This lambda is designed to be setup as a view or origin request in CloudFront given the
# preceding architecture. The function will redirect client requests for blobs from S3 to R2.
# Even if the blob does not exist on R2, it will read the file from S3, store it in R2,
# and return content to the client.
# Information about incremental update: https://developers.cloudflare.com/r2/data-migration/sippy/ .
#
# The only egress cost from AWS should be, then, the transfer from S3->R2 for the initial
# caching of the blob. Subsequent requests should have no egress costs (until the image is
# TTL pruned by the R2 management rule, which will cause it to be transferred from S3->R2
# if it is requested again).
#
# The lambda can be used on build farms and app.ci. When used on a build farm, the lambda will
# first check to see if the requested blob exists in the app.ci R2 bucket. If it does,
# the client will be redirected to the app.ci R2 bucket. This is to prevent unnecessary
# duplicate storage of images in each build farm R2 bucket (i.e. if it is available already
# in app.ci's R2, why cache it in each build farm's?).
#
# The app.ci AWS account AND the AWS account running build farms (or more precisely, any
# account running this lambda) must have a secret named "prod/lambda/r2-ci-registry-blob-caches-read-only"
# in their us-east-1 AWS Secret Manager store. This secret must contain
# access key and endpoint information authorized to read the R2 blob cache buckets.

from typing import Dict, List
from urllib.parse import quote, unquote_plus
import boto3
from botocore.client import Config
import ipaddress


# Ensure s3v4 signature is used regardless of the region the lambda is executing in.
BOTO3_CLIENT_CONFIG = Config(signature_version='s3v4')
# According to https://docs.aws.amazon.com/codeguru/detector-library/python/lambda-client-reuse/
# s3 clients can and should be reused. This allows the client to be cached in an execution
# environment and reused if possible. Initialize these lazily so we can handle ANY s3 errors
# inline with the request.
s3_client = None
secrets_client = None


def get_secrets_manager_secret_dict(secret_name):
    """
    Reads a secret value from AWS Secrets Manager in us-east-1
    :param secret_name: The name of the secret to read
    :return: The secret, parsed as a python dict.
    """
    global secrets_client

    import json  # lazy load to avoid a largish library if lambda does not need secret access

    if secrets_client is None:
        # We need to read in the secret from AWS SecretManager. No authentication
        # or endpoint information is required because the lambda is running with
        # a role that allows access to necessary secrets.
        secrets_client = boto3.client(
            service_name='secretsmanager',
            region_name='us-east-1'
        )

    try:
        get_secret_value_response = secrets_client.get_secret_value(
            SecretId=secret_name
        )
    except:
        raise

    # Assume it is a key/value pair secret and parse as json
    username_password_keypairs_str = get_secret_value_response['SecretString']
    return json.loads(username_password_keypairs_str)


def redirect(uri: str, code: int = 302, description="Found"):
    return {
        'status': code,
        'statusDescription': description,
        'headers': {
            "location": [{
                'key': 'Location',
                "value": str(uri)
            }],
        }
    }


APP_CI_DISTRIBUTION = 'E2KP8SMSY4XB67'

# Each CloudFront distribution should be mapped to an R2 bucket that is then
# setup to do incremental updates from the associated cluster's actual S3 bucket.
DISTRIBUTION_TO_R2_BUCKET_NAME = {
    APP_CI_DISTRIBUTION: 'app-ci-image-registry-blob-cache',
    'E1Q1256FT1FBYD': 'build01-image-registry-blob-cache',
    'E2PBG0JIU6CTJY': 'build03-image-registry-blob-cache',
    'E1PPY7S6SRDS9W': 'build05-image-registry-blob-cache',
    'E2B105Z8OCWZSC': 'build09-image-registry-blob-cache',
    'E2N1Y2UGVWY8LA': 'build10-image-registry-blob-cache',
}


def get_r2_s3_client():
    global s3_client

    # If we have not initialized the "s3 client" for R2 bucket access, do so now.
    # ALL R2 buckets must reside in the same CloudFlare account and be accessible
    # with the same ACCESS_KEY_ID/SECRET_ACCESS_KEY provided by CloudFlare.
    # These credentials will be stored in AWS in Secret Manager so that they
    # can be read in by the lambda when necessary.
    if s3_client is None:
        cloudflare_r2_bucket_info = get_secrets_manager_secret_dict('prod/lambda/r2-ci-registry-blob-caches-read-only')
        s3_client = boto3.client(
            "s3",
            aws_access_key_id=cloudflare_r2_bucket_info['AWS_ACCESS_KEY_ID'],
            aws_secret_access_key=cloudflare_r2_bucket_info['AWS_SECRET_ACCESS_KEY'],
            endpoint_url=cloudflare_r2_bucket_info['AWS_ENDPOINT_URL'],
            region_name='us-east-1',
            config=BOTO3_CLIENT_CONFIG)

    return s3_client


def lambda_handler(event: Dict, context: Dict):
    request: Dict = event['Records'][0]['cf']['request']
    uri: str = request['uri']
    event_config = event['Records'][0]['cf']['config']
    distribution_name = event_config['distributionId']
    request_ip = request['clientIp']

    # There is presently an issue with vsphere where it is improperly resolving
    # cloudflare IP addresses. vsphere is reaching out from IBM and with a
    # CIDR 169.59.196.160/28 .
    # If we see an IP in this range, serve the request from CloudFront instead
    # of R2 -- until the vsphere environment can be fixed to correctly resolve
    # the IP address of R2 hostnames.
    if ipaddress.ip_address(request_ip) in ipaddress.ip_network('169.59.196.160/28'):
        return request

    request_method = request.get('method', None)
    if request_method.lower() != "get":
        # The S3 signed URL is only for GET operations.
        # The registry itself will issue HEAD when checking for image blobs - particularly during
        # an operation like a skopeo copy.
        # Just let CloudFront & S3 origin handle these for now.
        # If we ever want to get to a shared blob cache to avoid redundant storage in
        # each registry's R2 bucket, we will need to handle the 'HEAD' calls in the lamdba itself
        # and respond to it directly based on whether we find the file in blob cache. This will
        # reduce the total amount of storage required for our registry R2 buckets, will also
        # mean that each registry's R2 bucket will be, in isolation, incomplete (i.e.
        # there would be blobs missing in each registry's R2 bucket that could only
        # be found in the shared blob cache). This creates risk I'd like to avoid
        # until we know we will be sticking with R2 for a long time.
        # For now, each registry will be associated with a single R2 bucket. Those
        # R2 buckets will have significant duplication (just as they always have).
        return request

    if not uri.startswith('/docker/registry/v2/blobs/'):
        # If this is not a blob, it is mutable (e.g. the value of a tag).
        # We cannot cache it in R2 because the internal registry is free to update
        # it at any time. For these, allow the request to pass through to be
        # serviced by CloudFront/S3 directly.
        return request

    if distribution_name not in DISTRIBUTION_TO_R2_BUCKET_NAME:
        # If there is not mapping to R2 for this distribution, just allow CloudFront
        # to process this request. This build farm will be expensive to run until
        # this is addressed.
        return request

    # If we have not initialized an R2 client, do so now.
    s3 = get_r2_s3_client()
    file_key = unquote_plus(uri.lstrip('/'))  # Strip '/' prefix and decode any uri encoding like "%2B"

    # Try to find the file in the R2 bucket associated with the actual distribution.
    target_bucket_name = DISTRIBUTION_TO_R2_BUCKET_NAME[distribution_name]

    try:
        s3.head_object(Bucket=target_bucket_name, Key=file_key)
    except:
        # Pass through to CloudFront for the likely 404
        return request

    # Otherwise, we found the file. Return a pre-signed URL to
    # let give the client access.
    url = s3.generate_presigned_url(
        ClientMethod='get_object',
        Params={
            'Bucket': target_bucket_name,
            'Key': file_key,
        },
        ExpiresIn=20 * 60,  # Expire in 20 minutes
    )

    # Redirect the request to S3 bucket for cost management
    return redirect(url, code=307, description='S3Redirect')
